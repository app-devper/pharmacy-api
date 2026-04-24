package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type SaleHandler struct{ dbm *db.Manager }

func NewSaleHandler(d *db.Manager) *SaleHandler { return &SaleHandler{dbm: d} }

var errSaleAlreadyVoided = errors.New("sale already voided")

type preparedSaleItem struct {
	Drug          models.Drug
	DrugID        bson.ObjectID
	Qty           int     // BASE units
	Price         float64 // per BASE unit, post item-discount
	OriginalPrice float64
	ItemDiscount  float64
	Subtotal      float64
	CostSubtotal  float64
	Unit          string // alt-unit display name ("" = base)
	UnitFactor    int    // 1 = base unit; >=2 = alt
	PriceTier     string // "" | retail | regular | wholesale
	// Forwarded from SaleItemInput — lets applySaleItem compare the client's
	// expected lot against the actual FEFO deduction.
	LotSnapshot *models.LotSnapshot
	// When true, applySaleItem records any stock shortfall as OversoldQty
	// instead of failing. Reconciled against the next import lot.
	AllowOversell bool
}

func (h *SaleHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limitStr := q.Get("limit")
	limit := int64(200)
	if l, err := strconv.ParseInt(limitStr, 10, 64); err == nil && l > 0 {
		limit = l
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tz := loadTimezone(ctx, mdb)

	filter := bson.M{}

	// Date range filter
	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr != "" || toStr != "" {
		dateFilter := bson.M{}
		if fromStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", fromStr, tz); err == nil {
				dateFilter["$gte"] = t
			}
		}
		if toStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", toStr, tz); err == nil {
				dateFilter["$lt"] = t.Add(24 * time.Hour)
			}
		}
		filter["sold_at"] = dateFilter
	}

	// Search by bill_no or customer_name
	if search := q.Get("q"); search != "" {
		escapedSearch := regexp.QuoteMeta(search)
		filter["$or"] = bson.A{
			bson.M{"bill_no": bson.M{"$regex": escapedSearch, "$options": "i"}},
			bson.M{"customer_name": bson.M{"$regex": escapedSearch, "$options": "i"}},
		}
	}

	cur, err := mdb.Sales().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "sold_at", Value: -1}}).SetLimit(limit),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var sales []models.Sale
	if err := cur.All(ctx, &sales); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sales == nil {
		sales = []models.Sale{}
	}
	jsonOK(w, sales)
}

func (h *SaleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.SaleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(input.Items) == 0 {
		jsonError(w, "items is required", http.StatusBadRequest)
		return
	}
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if input.ClientRequestID != "" {
		if handled := h.writeExistingSaleResponse(ctx, mdb, input.ClientRequestID, w); handled {
			return
		}
	}

	preparedItems, subtotal, err := h.prepareSaleItems(ctx, mdb, input.Items)
	if err != nil {
		if errors.Is(err, bson.ErrInvalidHex) {
			jsonError(w, "invalid drug id", http.StatusBadRequest)
			return
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "drug not found", http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	customerID, customerName, err := h.resolveSaleCustomer(ctx, mdb, input.CustomerID)
	if err != nil {
		if errors.Is(err, bson.ErrInvalidHex) || errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "customer not found", http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	discount := math.Max(0, math.Min(input.Discount, subtotal))
	total := subtotal - discount
	received := input.Received
	if received == 0 {
		received = total
	}
	if received < total {
		jsonError(w, "received must be >= total", http.StatusBadRequest)
		return
	}
	change := math.Max(0, received-total)

	// Bill number is keyed by calendar day in the pharmacy's timezone so same-day
	// sales share one counter and the YYMMDD prefix matches the local date.
	tz := loadTimezone(ctx, mdb)
	var billNo string
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now().In(tz)
		generatedBillNo, err := h.nextSaleBillNo(txCtx, mdb, now)
		if err != nil {
			return err
		}

		sale := models.Sale{
			BillNo:          generatedBillNo,
			ClientRequestID: input.ClientRequestID,
			CustomerID:      customerID,
			CustomerName:    customerName,
			Discount:        discount,
			Total:           total,
			Received:        received,
			Change:          change,
			SoldAt:          now,
		}
		res, err := mdb.Sales().InsertOne(txCtx, sale)
		if err != nil {
			return err
		}
		saleOID := res.InsertedID.(bson.ObjectID)

		for _, item := range preparedItems {
			if err := h.applySaleItem(txCtx, mdb, saleOID, item); err != nil {
				return err
			}
		}

		if customerID != nil {
			updateRes, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": customerID},
				bson.M{
					"$inc": bson.M{"total_spent": total},
					"$set": bson.M{"last_visit": now},
				},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}
		}

		billNo = generatedBillNo
		return nil
	}); err != nil {
		if input.ClientRequestID != "" && isMongoDuplicate(err) {
			if handled := h.writeExistingSaleResponse(ctx, mdb, input.ClientRequestID, w); handled {
				return
			}
		}
		switch {
		case errors.Is(err, mongo.ErrNoDocuments):
			jsonError(w, "drug not found", http.StatusBadRequest)
		default:
			jsonError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Collect fresh stock values for each drug we just sold so the client can patch
	// local state without hitting GET /api/pharmacy/v1/drugs again. One query per unique drug.
	updates := make([]models.StockUpdate, 0, len(preparedItems))
	seen := make(map[bson.ObjectID]struct{}, len(preparedItems))
	for _, it := range preparedItems {
		if _, ok := seen[it.DrugID]; ok {
			continue
		}
		seen[it.DrugID] = struct{}{}
		var d struct {
			Stock int `bson:"stock"`
		}
		if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": it.DrugID},
			options.FindOne().SetProjection(bson.M{"stock": 1}),
		).Decode(&d); err == nil {
			updates = append(updates, models.StockUpdate{DrugID: it.DrugID, NewStock: d.Stock})
		}
	}

	jsonOK(w, models.SaleResponse{
		BillNo: billNo, Discount: discount, Total: total, Change: change,
		StockUpdates: updates,
	})
}

func (h *SaleHandler) writeExistingSaleResponse(ctx context.Context, mdb *db.MongoDB, clientRequestID string, w http.ResponseWriter) bool {
	var sale models.Sale
	err := mdb.Sales().FindOne(ctx, bson.M{"client_request_id": clientRequestID}).Decode(&sale)
	if err != nil {
		return false
	}
	jsonOK(w, models.SaleResponse{
		BillNo:   sale.BillNo,
		Discount: sale.Discount,
		Total:    sale.Total,
		Change:   sale.Change,
	})
	return true
}

func (h *SaleHandler) prepareSaleItems(ctx context.Context, mdb *db.MongoDB, inputs []models.SaleItemInput) ([]preparedSaleItem, float64, error) {
	items := make([]preparedSaleItem, 0, len(inputs))
	requiredByDrug := make(map[bson.ObjectID]int)
	var subtotal float64

	for _, input := range inputs {
		if input.Qty <= 0 {
			return nil, 0, fmt.Errorf("qty must be > 0")
		}
		if input.Price < 0 {
			return nil, 0, fmt.Errorf("price must be >= 0")
		}
		if input.OriginalPrice < 0 {
			return nil, 0, fmt.Errorf("original_price must be >= 0")
		}
		if input.ItemDiscount < 0 {
			return nil, 0, fmt.Errorf("item_discount must be >= 0")
		}

		drugID, err := bson.ObjectIDFromHex(input.DrugID)
		if err != nil {
			return nil, 0, err
		}

		var drug models.Drug
		if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugID}).Decode(&drug); err != nil {
			return nil, 0, err
		}

		// Multi-unit: when the client ships a non-empty Unit, verify it matches an
		// AltUnit on the drug and that the supplied Qty (which is ALWAYS in base
		// units) is a multiple of the factor. UnitFactor = 0 or 1 = base unit.
		unit := strings.TrimSpace(input.Unit)
		factor := input.UnitFactor
		var matchedAlt *models.AltUnit
		if unit != "" {
			for i := range drug.AltUnits {
				if drug.AltUnits[i].Name == unit {
					matchedAlt = &drug.AltUnits[i]
					break
				}
			}
			if matchedAlt == nil {
				return nil, 0, fmt.Errorf("unit %q ไม่พบในยา %s", unit, drug.Name)
			}
			if factor == 0 {
				factor = matchedAlt.Factor
			} else if factor != matchedAlt.Factor {
				return nil, 0, fmt.Errorf("unit_factor ไม่ตรงกับ alt_unit ของยา %s", drug.Name)
			}
			if input.Qty%factor != 0 {
				return nil, 0, fmt.Errorf("qty (%d) ต้องหารด้วย factor (%d) ลงตัว สำหรับยา %s", input.Qty, factor, drug.Name)
			}
		} else {
			factor = 1 // base unit
		}

		// Pricing tier: authoritative price comes from the drug document, not
		// the client's claimed `original_price`. This closes the "client sets
		// tier=wholesale but sends retail amount" loophole.
		tier := strings.TrimSpace(input.PriceTier)
		if !isValidPriceTier(tier) {
			return nil, 0, fmt.Errorf("price_tier ของยา %s ไม่ถูกต้อง", drug.Name)
		}
		var authoritativeOriginal float64
		if matchedAlt != nil {
			perAlt := resolveTierPrice(matchedAlt.SellPrice, matchedAlt.Prices, tier)
			// Round to 2 decimal places so fractional division (e.g. ฿100/3)
			// doesn't leave long-tail floats on the SaleItem that later break
			// reprint / receipt math.
			authoritativeOriginal = math.Round(perAlt/float64(factor)*100) / 100
		} else {
			authoritativeOriginal = resolveTierPrice(drug.SellPrice, drug.Prices, tier)
		}

		originalPrice := authoritativeOriginal
		itemDiscount := input.ItemDiscount
		effectivePrice := originalPrice - itemDiscount
		if effectivePrice < 0 {
			effectivePrice = 0
		}
		subtotal += effectivePrice * float64(input.Qty)
		// Oversold inputs skip the stock-availability aggregation — the apply
		// step records any shortfall as OversoldQty instead of failing.
		if !input.AllowOversell {
			requiredByDrug[drugID] += input.Qty
		}
		items = append(items, preparedSaleItem{
			Drug:          drug,
			DrugID:        drugID,
			Qty:           input.Qty,
			Price:         effectivePrice,
			OriginalPrice: originalPrice,
			ItemDiscount:  itemDiscount,
			Subtotal:      effectivePrice * float64(input.Qty),
			Unit:          unit,
			UnitFactor:    factor,
			PriceTier:     tier,
			LotSnapshot:   input.LotSnapshot,
			AllowOversell: input.AllowOversell,
		})
	}

	for drugID, need := range requiredByDrug {
		if err := h.ensureSaleInventoryAvailable(ctx, mdb, drugID, need); err != nil {
			return nil, 0, err
		}
	}

	return items, subtotal, nil
}

func (h *SaleHandler) ensureSaleInventoryAvailable(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, need int) error {
	var drug models.Drug
	if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugID}).Decode(&drug); err != nil {
		return err
	}
	if drug.Stock < need {
		return fmt.Errorf("insufficient stock for %s", drug.Name)
	}

	cur, err := mdb.DrugLots().Find(ctx,
		bson.M{"drug_id": drugID, "remaining": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}}),
	)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	totalRemaining := 0
	lotCount := 0
	for cur.Next(ctx) {
		var lot models.DrugLot
		if err := cur.Decode(&lot); err != nil {
			return err
		}
		lotCount++
		totalRemaining += lot.Remaining
	}
	if err := cur.Err(); err != nil {
		return err
	}
	if lotCount > 0 && totalRemaining < need {
		return fmt.Errorf("insufficient lot inventory for %s", drug.Name)
	}
	return nil
}

func (h *SaleHandler) resolveSaleCustomer(ctx context.Context, mdb *db.MongoDB, customerID *string) (*bson.ObjectID, string, error) {
	if customerID == nil || *customerID == "" {
		return nil, "", nil
	}

	oid, err := bson.ObjectIDFromHex(*customerID)
	if err != nil {
		return nil, "", err
	}

	var customer models.Customer
	if err := mdb.Customers().FindOne(ctx, bson.M{"_id": oid}).Decode(&customer); err != nil {
		return nil, "", err
	}
	return &oid, customer.Name, nil
}

func (h *SaleHandler) nextSaleBillNo(ctx context.Context, mdb *db.MongoDB, now time.Time) (string, error) {
	today := now.Format("060102")
	counterID := "INV-" + today
	var counter struct {
		Seq int `bson:"seq"`
	}
	err := mdb.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": counterID},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return "", fmt.Errorf("bill number error: %w", err)
	}
	return fmt.Sprintf("INV-%s-%03d", today, counter.Seq), nil
}

func (h *SaleHandler) applySaleItem(ctx context.Context, mdb *db.MongoDB, saleID bson.ObjectID, item preparedSaleItem) error {
	// Oversell-aware stock decrement.
	//  • Normal path: $gte guard — refuses if stock < qty (prevents accidental
	//    negative on mis-click).
	//  • Oversell path: unconditional $inc — drug.stock may go negative. The
	//    shortfall is tracked as OversoldQty on the SaleItem and will be
	//    reconciled when a future import lands for this drug.
	if item.AllowOversell {
		if _, err := mdb.Drugs().UpdateOne(ctx,
			bson.M{"_id": item.DrugID},
			bson.M{"$inc": bson.M{"stock": -item.Qty}},
		); err != nil {
			return err
		}
	} else {
		updateResult, err := mdb.Drugs().UpdateOne(ctx,
			bson.M{"_id": item.DrugID, "stock": bson.M{"$gte": item.Qty}},
			bson.M{"$inc": bson.M{"stock": -item.Qty}},
		)
		if err != nil {
			return err
		}
		if updateResult.MatchedCount == 0 {
			return fmt.Errorf("insufficient stock for %s", item.Drug.Name)
		}
	}

	lotCur, err := mdb.DrugLots().Find(ctx,
		bson.M{"drug_id": item.DrugID, "remaining": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}}),
	)
	if err != nil {
		return err
	}
	defer lotCur.Close(ctx)

	var lots []models.DrugLot
	if err := lotCur.All(ctx, &lots); err != nil {
		return err
	}
	if len(lots) == 0 {
		// No lots available at all. In oversell mode this is expected (classic
		// "zero-inventory sale"); otherwise fall back to pre-lot legacy mode
		// and trust drug.stock only. Either way we record all of item.Qty as
		// OversoldQty when oversell was opted in, so the next import can
		// reconcile lot_splits retroactively.
		oversold := 0
		if item.AllowOversell {
			oversold = item.Qty
		}
		si := models.SaleItem{
			SaleID:        saleID,
			DrugID:        item.DrugID,
			DrugName:      item.Drug.Name,
			Qty:           item.Qty,
			Price:         item.Price,
			OriginalPrice: item.OriginalPrice,
			ItemDiscount:  item.ItemDiscount,
			Subtotal:      item.Subtotal,
			CostSubtotal:  float64(item.Qty) * item.Drug.CostPrice,
			Unit:          item.Unit,
			UnitFactor:    item.UnitFactor,
			PriceTier:     item.PriceTier,
			LotSnapshot:   item.LotSnapshot,
			OversoldQty:   oversold,
		}
		if _, err := mdb.SaleItems().InsertOne(ctx, si); err != nil {
			return err
		}
		return nil
	}

	need := item.Qty
	costSubtotal := 0.0
	splits := make([]models.LotDeduction, 0, 2)
	for _, lot := range lots {
		if need <= 0 {
			break
		}

		deduct := lot.Remaining
		if deduct > need {
			deduct = need
		}
		res, err := mdb.DrugLots().UpdateOne(ctx,
			bson.M{"_id": lot.ID, "remaining": bson.M{"$gte": deduct}},
			bson.M{"$inc": bson.M{"remaining": -deduct}},
		)
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return fmt.Errorf("insufficient lot inventory for %s", item.Drug.Name)
		}
		lotCost := item.Drug.CostPrice
		if lot.CostPrice != nil {
			lotCost = *lot.CostPrice
		}
		costSubtotal += float64(deduct) * lotCost
		need -= deduct
		splits = append(splits, models.LotDeduction{
			LotID:      lot.ID,
			LotNumber:  lot.LotNumber,
			ExpiryDate: lot.ExpiryDate,
			Qty:        deduct,
		})
	}
	if need > 0 {
		// Ran out of lots before satisfying item.Qty. Only acceptable in
		// oversell mode — the remainder becomes an unreconciled debt that
		// the next import for this drug will absorb.
		if !item.AllowOversell {
			return fmt.Errorf("insufficient lot inventory for %s", item.Drug.Name)
		}
		costSubtotal += float64(need) * item.Drug.CostPrice
	}
	oversold := 0
	if item.AllowOversell && need > 0 {
		oversold = need
	}

	// Compliance reconciliation: flag when the client's expected lot (captured
	// at cart checkout) differs from the first lot FEFO actually pulled from.
	// This happens most commonly when an offline-queued sale syncs after
	// another terminal has shifted the FEFO queue. The sale still succeeds —
	// pharmacists just get a hint that the paper audit trail may need review.
	lotMismatch := false
	if item.LotSnapshot != nil && len(splits) > 0 {
		lotMismatch = splits[0].LotID != item.LotSnapshot.LotID
	}

	si := models.SaleItem{
		SaleID:        saleID,
		DrugID:        item.DrugID,
		DrugName:      item.Drug.Name,
		Qty:           item.Qty,
		Price:         item.Price,
		OriginalPrice: item.OriginalPrice,
		ItemDiscount:  item.ItemDiscount,
		Subtotal:      item.Subtotal,
		CostSubtotal:  costSubtotal,
		Unit:          item.Unit,
		UnitFactor:    item.UnitFactor,
		PriceTier:     item.PriceTier,
		LotSplits:     splits,
		LotSnapshot:   item.LotSnapshot,
		LotMismatch:   lotMismatch,
		OversoldQty:   oversold,
	}
	if _, err := mdb.SaleItems().InsertOne(ctx, si); err != nil {
		return err
	}

	return nil
}

func (h *SaleHandler) Items(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := mdb.SaleItems().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var items []models.SaleItem
	if err := cur.All(ctx, &items); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []models.SaleItem{}
	}
	jsonOK(w, items)
}

// Void cancels a sale: marks it voided, restores drug stock, reverses customer spend.
func (h *SaleHandler) Void(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Fetch sale
	var sale models.Sale
	if err := mdb.Sales().FindOne(ctx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		jsonError(w, "sale not found", http.StatusNotFound)
		return
	}
	if sale.Voided {
		jsonError(w, "sale already voided", http.StatusConflict)
		return
	}

	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now()
		updateRes, err := mdb.Sales().UpdateOne(txCtx,
			bson.M{"_id": oid, "voided": bson.M{"$ne": true}},
			bson.M{"$set": bson.M{
				"voided":      true,
				"void_reason": body.Reason,
				"voided_at":   now,
			}},
		)
		if err != nil {
			return err
		}
		if updateRes.MatchedCount == 0 {
			return errSaleAlreadyVoided
		}

		itemCur, err := mdb.SaleItems().Find(txCtx, bson.M{"sale_id": oid})
		if err != nil {
			return err
		}
		defer itemCur.Close(txCtx)

		var items []models.SaleItem
		if err := itemCur.All(txCtx, &items); err != nil {
			return err
		}

		retCur, err := mdb.DrugReturns().Find(txCtx, bson.M{"sale_id": oid})
		if err != nil {
			return err
		}
		defer retCur.Close(txCtx)

		var returns []models.DrugReturn
		if err := retCur.All(txCtx, &returns); err != nil {
			return err
		}

		returnedByItem := make(map[bson.ObjectID]int, len(items))
		refunded := 0.0
		for _, ret := range returns {
			refunded += ret.Refund
			for _, item := range ret.Items {
				returnedByItem[item.SaleItemID] += item.Qty
			}
		}

		for _, item := range items {
			restoreQty := item.Qty - returnedByItem[item.ID]
			if restoreQty <= 0 {
				continue
			}

			if _, err := mdb.Drugs().UpdateOne(txCtx,
				bson.M{"_id": item.DrugID},
				bson.M{"$inc": bson.M{"stock": restoreQty}},
			); err != nil {
				return err
			}

			// Only restore to real lots the portion of the sale that was
			// actually deducted from a lot. LotSplits tell the truth:
			//  • Real splits (non-zero LotID) — from lots at sale or import
			//    reconcile → reverse back to those lots.
			//  • Synthetic splits (LotID == zero) — from stock adjustments
			//    → no lot to give back to; drug.stock was already credited
			//    above, nothing else to do.
			//  • Unreconciled OversoldQty — no lot ever assigned; also just
			//    forgiven via the stock credit.
			// Prior returns are assumed to have eaten the real-lot portion
			// first (worst case), so we subtract returned qty from it too.
			lotCovered := 0
			for _, sp := range item.LotSplits {
				if !sp.LotID.IsZero() {
					lotCovered += sp.Qty
				}
			}
			lotCovered -= returnedByItem[item.ID]
			if lotCovered > restoreQty {
				lotCovered = restoreQty
			}
			if lotCovered > 0 {
				if err := restoreSaleItemLots(txCtx, mdb, item, lotCovered, returnedByItem[item.ID]); err != nil {
					return err
				}
			}
		}

		if sale.CustomerID != nil {
			remainingSpend := sale.Total - refunded
			if remainingSpend <= 0 {
				return nil
			}
			updateRes, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": sale.CustomerID},
				bson.M{"$inc": bson.M{"total_spent": -remainingSpend}},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}
		}

		return nil
	}); err != nil {
		if errors.Is(err, errSaleAlreadyVoided) {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "referenced document not found", http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

func restoreSaleItemLots(ctx context.Context, mdb *db.MongoDB, item models.SaleItem, qty int, skipRealLotQty int) error {
	need := qty
	skip := skipRealLotQty
	for _, split := range item.LotSplits {
		if need <= 0 {
			break
		}
		if split.LotID.IsZero() || split.Qty <= 0 {
			continue
		}

		availableFromSplit := split.Qty
		if skip > 0 {
			if skip >= availableFromSplit {
				skip -= availableFromSplit
				continue
			}
			availableFromSplit -= skip
			skip = 0
		}
		restore := availableFromSplit
		if restore > need {
			restore = need
		}

		res, err := mdb.DrugLots().UpdateOne(ctx,
			bson.M{"_id": split.LotID, "drug_id": item.DrugID},
			bson.M{"$inc": bson.M{"remaining": restore}},
		)
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return fmt.Errorf("failed to restore lot inventory for %s", item.DrugName)
		}

		need -= restore
	}

	if need > 0 {
		return fmt.Errorf("failed to fully restore lot inventory for %s", item.DrugName)
	}

	return nil
}

// reconcileOversoldFromAdjustment drains up to `available` base units from
// pending oversold SaleItems when the admin bumps stock via a manual
// adjustment (not an import). Returns the total qty actually drained so the
// caller can offset drug.stock accordingly.
//
// Unlike reconcileOversold — which absorbs into a real DrugLot and records
// the lot_id/expiry on lot_splits — this path has no lot to attribute to.
// We still append a synthetic LotDeduction to lot_splits so the audit trail
// is complete; it uses a zero LotID and a LotNumber of "ADJUST:<reason>" so
// pharmacists can tell at a glance that this portion was reconciled by an
// adjustment rather than an import. Callers MUST already be inside a
// transaction so drug.stock and oversold_qty stay in sync on failure.
func reconcileOversoldFromAdjustment(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, available int, reason string) (int, error) {
	if available <= 0 {
		return 0, nil
	}
	cur, err := mdb.SaleItems().Find(ctx,
		bson.M{"drug_id": drugID, "oversold_qty": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}),
	)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)
	var items []models.SaleItem
	if err := cur.All(ctx, &items); err != nil {
		return 0, err
	}

	remaining := available
	drained := 0
	marker := "ADJUST"
	if reason != "" {
		marker = "ADJUST:" + reason
	}
	now := time.Now()
	for _, si := range items {
		if remaining <= 0 {
			break
		}
		take := si.OversoldQty
		if take > remaining {
			take = remaining
		}
		synthetic := models.LotDeduction{
			LotID:      bson.NilObjectID,
			LotNumber:  marker,
			ExpiryDate: now, // no real expiry; use now to keep field non-zero
			Qty:        take,
		}
		if _, err := mdb.SaleItems().UpdateOne(ctx,
			bson.M{"_id": si.ID},
			bson.M{
				"$inc":  bson.M{"oversold_qty": -take},
				"$push": bson.M{"lot_splits": synthetic},
			},
		); err != nil {
			return drained, err
		}
		remaining -= take
		drained += take
	}
	return drained, nil
}

// reconcileOversold drains `lot`'s remaining against older SaleItems that were
// sold on credit (AllowOversell=true) and never got a matching lot deduction.
//
// Processed in sold_at ASC order — the oldest debt gets paid first. For each
// eligible SaleItem the function:
//  1. Chooses `drain = min(lot.remaining, si.oversold_qty)`.
//  2. Decrements the lot via `$inc remaining -drain` with a `$gte` guard.
//  3. Decrements `oversold_qty` on the SaleItem and appends a LotDeduction
//     to `lot_splits` so the audit trail for that sale becomes complete.
//
// drug.stock is deliberately NOT touched — the oversold sale already debited
// it at sale time, and the caller just credited it with the full lot.Qty.
// Stops early when the lot is empty.
//
// Callers must already be inside a transaction so a partial drain doesn't leak.
func reconcileOversold(ctx context.Context, mdb *db.MongoDB, drugID bson.ObjectID, lot models.DrugLot) error {
	remaining := lot.Remaining
	if remaining <= 0 {
		return nil
	}
	cur, err := mdb.SaleItems().Find(ctx,
		bson.M{"drug_id": drugID, "oversold_qty": bson.M{"$gt": 0}},
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}), // ObjectID ≈ insertion order ≈ sold_at ASC
	)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	var items []models.SaleItem
	if err := cur.All(ctx, &items); err != nil {
		return err
	}

	for _, si := range items {
		if remaining <= 0 {
			break
		}
		drain := si.OversoldQty
		if drain > remaining {
			drain = remaining
		}

		// Decrement the lot with $gte guard — protects against a concurrent
		// reconcile draining it first.
		lotRes, err := mdb.DrugLots().UpdateOne(ctx,
			bson.M{"_id": lot.ID, "remaining": bson.M{"$gte": drain}},
			bson.M{"$inc": bson.M{"remaining": -drain}},
		)
		if err != nil {
			return err
		}
		if lotRes.MatchedCount == 0 {
			// Someone else drained it; stop — the outer reconcile for the
			// next lot (if any) will pick up the slack.
			break
		}

		// Append to this SaleItem's audit trail and reduce its debt.
		split := models.LotDeduction{
			LotID:      lot.ID,
			LotNumber:  lot.LotNumber,
			ExpiryDate: lot.ExpiryDate,
			Qty:        drain,
		}
		if _, err := mdb.SaleItems().UpdateOne(ctx,
			bson.M{"_id": si.ID},
			bson.M{
				"$inc":  bson.M{"oversold_qty": -drain},
				"$push": bson.M{"lot_splits": split},
			},
		); err != nil {
			return err
		}
		remaining -= drain
	}
	return nil
}
