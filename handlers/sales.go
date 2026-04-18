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
	Drug         models.Drug
	DrugID       bson.ObjectID
	Qty          int
	Price        float64
	Subtotal     float64
	CostSubtotal float64
}

func (h *SaleHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limitStr := q.Get("limit")
	limit := int64(200)
	if l, err := strconv.ParseInt(limitStr, 10, 64); err == nil && l > 0 {
		limit = l
	}

	filter := bson.M{}

	// Date range filter
	fromStr := q.Get("from")
	toStr := q.Get("to")
	if fromStr != "" || toStr != "" {
		dateFilter := bson.M{}
		if fromStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", fromStr, time.Local); err == nil {
				dateFilter["$gte"] = t
			}
		}
		if toStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", toStr, time.Local); err == nil {
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

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

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

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

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

	var billNo string
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now()
		generatedBillNo, err := h.nextSaleBillNo(txCtx, mdb, now)
		if err != nil {
			return err
		}

		sale := models.Sale{
			BillNo:       generatedBillNo,
			CustomerID:   customerID,
			CustomerName: customerName,
			Discount:     discount,
			Total:        total,
			Received:     received,
			Change:       change,
			SoldAt:       now,
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
			if _, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": customerID},
				bson.M{
					"$inc": bson.M{"total_spent": total},
					"$set": bson.M{"last_visit": now},
				},
			); err != nil {
				return err
			}
		}

		billNo = generatedBillNo
		return nil
	}); err != nil {
		switch {
		case errors.Is(err, mongo.ErrNoDocuments):
			jsonError(w, "drug not found", http.StatusBadRequest)
		default:
			jsonError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	jsonOK(w, models.SaleResponse{BillNo: billNo, Discount: discount, Total: total, Change: change})
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

		drugID, err := bson.ObjectIDFromHex(input.DrugID)
		if err != nil {
			return nil, 0, err
		}

		var drug models.Drug
		if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": drugID}).Decode(&drug); err != nil {
			return nil, 0, err
		}
		subtotal += input.Price * float64(input.Qty)
		requiredByDrug[drugID] += input.Qty
		items = append(items, preparedSaleItem{
			Drug:     drug,
			DrugID:   drugID,
			Qty:      input.Qty,
			Price:    input.Price,
			Subtotal: input.Price * float64(input.Qty),
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
		si := models.SaleItem{
			SaleID:       saleID,
			DrugID:       item.DrugID,
			DrugName:     item.Drug.Name,
			Qty:          item.Qty,
			Price:        item.Price,
			Subtotal:     item.Subtotal,
			CostSubtotal: float64(item.Qty) * item.Drug.CostPrice,
		}
		if _, err := mdb.SaleItems().InsertOne(ctx, si); err != nil {
			return err
		}
		return nil
	}

	need := item.Qty
	costSubtotal := 0.0
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
	}
	if len(lots) > 0 && need > 0 {
		return fmt.Errorf("insufficient lot inventory for %s", item.Drug.Name)
	}

	si := models.SaleItem{
		SaleID:       saleID,
		DrugID:       item.DrugID,
		DrugName:     item.Drug.Name,
		Qty:          item.Qty,
		Price:        item.Price,
		Subtotal:     item.Subtotal,
		CostSubtotal: costSubtotal,
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

		for _, item := range items {
			if _, err := mdb.Drugs().UpdateOne(txCtx,
				bson.M{"_id": item.DrugID},
				bson.M{"$inc": bson.M{"stock": item.Qty}},
			); err != nil {
				return err
			}

			if err := h.restoreSaleItemLots(txCtx, mdb, item); err != nil {
				return err
			}
		}

		if sale.CustomerID != nil {
			if _, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": sale.CustomerID},
				bson.M{"$inc": bson.M{"total_spent": -sale.Total}},
			); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		if errors.Is(err, errSaleAlreadyVoided) {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]bool{"ok": true})
}

func (h *SaleHandler) restoreSaleItemLots(ctx context.Context, mdb *db.MongoDB, item models.SaleItem) error {
	// Restore to ASC (earliest-expiry first) to mirror FEFO deduction order
	lotCur, err := mdb.DrugLots().Find(ctx,
		bson.M{"drug_id": item.DrugID},
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

	need := item.Qty
	for _, lot := range lots {
		if need <= 0 {
			break
		}

		canRestore := lot.Quantity - lot.Remaining
		if canRestore <= 0 {
			continue
		}

		restore := canRestore
		if restore > need {
			restore = need
		}

		res, err := mdb.DrugLots().UpdateOne(ctx,
			bson.M{"_id": lot.ID, "remaining": bson.M{"$lte": lot.Quantity - restore}},
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

	if len(lots) > 0 && need > 0 {
		return fmt.Errorf("failed to fully restore lot inventory for %s", item.DrugName)
	}

	return nil
}
