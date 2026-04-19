package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type ImportHandler struct{ dbm *db.Manager }

func NewImportHandler(d *db.Manager) *ImportHandler { return &ImportHandler{dbm: d} }

type importLogEntry struct {
	DocNo     string
	LotNumber string
	DrugName  string
	Qty       int
}

var errPurchaseOrderNoLongerDraft = errors.New("purchase order is no longer draft")

// List returns all purchase orders (summary only, items excluded).
func (h *ImportHandler) List(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	filter := bson.M{}
	if s := r.URL.Query().Get("supplier"); s != "" {
		filter["supplier"] = s
	}

	cur, err := mdb.PurchaseOrders().Find(ctx, filter,
		options.Find().
			SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetLimit(200).
			SetProjection(bson.M{"items": 0}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var list []models.PurchaseOrderSummary
	if err := cur.All(ctx, &list); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []models.PurchaseOrderSummary{}
	}
	jsonOK(w, list)
}

// GetOne returns the full purchase order including items.
func (h *ImportHandler) GetOne(w http.ResponseWriter, r *http.Request) {
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

	var po models.PurchaseOrder
	if err := mdb.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po); err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	jsonOK(w, po)
}

// Create creates a new draft purchase order and generates an auto doc_no.
func (h *ImportHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.POInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
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
	tz := loadTimezone(ctx, mdb)

	// Generate atomic doc_no: IMP-YYMMDD-NNN (keyed by local calendar day).
	now := time.Now()
	today := now.In(tz).Format("060102")
	counterID := "IMP-" + today
	var counter struct {
		Seq int `bson:"seq"`
	}
	err = mdb.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": counterID},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		jsonError(w, "doc_no generation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	docNo := fmt.Sprintf("IMP-%s-%03d", today, counter.Seq)

	receiveDate := now
	if input.ReceiveDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", input.ReceiveDate, tz)
		if err != nil {
			jsonError(w, "receive_date must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		receiveDate = parsed
	}

	items, totalCost, err := h.buildItems(ctx, mdb, input.Items)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	po := models.PurchaseOrder{
		DocNo:       docNo,
		Supplier:    input.Supplier,
		InvoiceNo:   input.InvoiceNo,
		ReceiveDate: receiveDate,
		Items:       items,
		ItemCount:   len(items),
		TotalCost:   totalCost,
		Status:      "draft",
		Notes:       input.Notes,
		CreatedAt:   now,
	}

	res, err := mdb.PurchaseOrders().InsertOne(ctx, po)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	po.ID = res.InsertedID.(bson.ObjectID)
	jsonOK(w, po)
}

// Update replaces a draft purchase order's content.
func (h *ImportHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var input models.POInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
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

	// Guard: can only update drafts
	var existing models.PurchaseOrder
	if err := mdb.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&existing); err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if existing.Status != "draft" {
		jsonError(w, "cannot edit a confirmed order", http.StatusBadRequest)
		return
	}

	tz := loadTimezone(ctx, mdb)
	receiveDate := existing.ReceiveDate
	if input.ReceiveDate != "" {
		parsed, err := time.ParseInLocation("2006-01-02", input.ReceiveDate, tz)
		if err != nil {
			jsonError(w, "receive_date must be YYYY-MM-DD", http.StatusBadRequest)
			return
		}
		receiveDate = parsed
	}

	items, totalCost, err := h.buildItems(ctx, mdb, input.Items)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	res, err := mdb.PurchaseOrders().UpdateOne(ctx, bson.M{"_id": oid, "status": "draft"},
		bson.M{"$set": bson.M{
			"supplier":     input.Supplier,
			"invoice_no":   input.InvoiceNo,
			"receive_date": receiveDate,
			"notes":        input.Notes,
			"items":        items,
			"item_count":   len(items),
			"total_cost":   totalCost,
		}},
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if res.MatchedCount == 0 {
		jsonError(w, "cannot edit a confirmed order", http.StatusBadRequest)
		return
	}

	existing.Supplier = input.Supplier
	existing.InvoiceNo = input.InvoiceNo
	existing.ReceiveDate = receiveDate
	existing.Notes = input.Notes
	existing.Items = items
	existing.ItemCount = len(items)
	existing.TotalCost = totalCost
	jsonOK(w, existing)
}

// Confirm validates and confirms a draft order, creating DrugLots and incrementing stock.
func (h *ImportHandler) Confirm(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tz := loadTimezone(ctx, mdb)

	var po models.PurchaseOrder
	if err := mdb.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po); err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if po.Status != "draft" {
		jsonError(w, "already confirmed", http.StatusBadRequest)
		return
	}
	if len(po.Items) == 0 {
		jsonError(w, "no items to confirm", http.StatusBadRequest)
		return
	}

	// Validate all items before touching any lots or stock
	for i, item := range po.Items {
		if item.LotNumber == "" {
			jsonError(w, fmt.Sprintf("รายการที่ %d: กรุณาระบุล็อตหมายเลข", i+1), http.StatusBadRequest)
			return
		}
		if item.Qty <= 0 {
			jsonError(w, fmt.Sprintf("รายการที่ %d: จำนวนต้องมากกว่า 0", i+1), http.StatusBadRequest)
			return
		}
		if _, err := time.ParseInLocation("2006-01-02", item.ExpiryDate, tz); err != nil {
			jsonError(w, fmt.Sprintf("รายการที่ %d: วันหมดอายุไม่ถูกต้อง", i+1), http.StatusBadRequest)
			return
		}
	}

	var logEntries []importLogEntry
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now()
		receiveDate := po.ReceiveDate.Format("2006-01-02")
		attemptLogs := make([]importLogEntry, 0, len(po.Items))

		for _, item := range po.Items {
			expiry, _ := time.ParseInLocation("2006-01-02", item.ExpiryDate, tz)
			costPrice := item.CostPrice

			lot := models.DrugLot{
				DrugID:     item.DrugID,
				DrugName:   item.DrugName,
				LotNumber:  item.LotNumber,
				ExpiryDate: expiry,
				ImportDate: po.ReceiveDate,
				CostPrice:  &costPrice,
				SellPrice:  item.SellPrice,
				Quantity:   item.Qty,
				Remaining:  item.Qty,
				CreatedAt:  now,
			}
			lotRes, err := mdb.DrugLots().InsertOne(txCtx, lot)
			if err != nil {
				return fmt.Errorf("lot insert failed for %s: %w", item.DrugName, err)
			}
			if oid, ok := lotRes.InsertedID.(bson.ObjectID); ok {
				lot.ID = oid
			}

			stockRes, err := mdb.Drugs().UpdateOne(txCtx,
				bson.M{"_id": item.DrugID},
				bson.M{"$inc": bson.M{"stock": item.Qty}},
			)
			if err != nil {
				return fmt.Errorf("stock update failed: %w", err)
			}
			if stockRes.MatchedCount == 0 {
				return fmt.Errorf("drug not found for %s", item.DrugName)
			}

			// Oversell reconciliation — absorb any pending "sell now, reconcile
			// later" debts against this new lot. Walks prior SaleItems with
			// oversold_qty > 0 in sale order and backfills their LotSplits.
			// drug.stock is intentionally NOT touched: the oversold sale
			// already decremented it; the +qty above brings the books to the
			// correct net position.
			if err := reconcileOversold(txCtx, mdb, item.DrugID, lot); err != nil {
				return fmt.Errorf("oversold reconcile failed for %s: %w", item.DrugName, err)
			}

			var drug models.Drug
			if err := mdb.Drugs().FindOne(txCtx, bson.M{"_id": item.DrugID}).Decode(&drug); err != nil {
				return fmt.Errorf("drug lookup failed for %s: %w", item.DrugName, err)
			}

			ky9 := models.Ky9{
				Date:         receiveDate,
				DrugName:     item.DrugName,
				RegNo:        drug.RegNo,
				Unit:         drug.Unit,
				Qty:          item.Qty,
				PricePerUnit: item.CostPrice,
				TotalValue:   float64(item.Qty) * item.CostPrice,
				Seller:       po.Supplier,
				InvoiceNo:    po.InvoiceNo,
				CreatedAt:    now,
			}
			if _, err := mdb.Ky9().InsertOne(txCtx, ky9); err != nil {
				return fmt.Errorf("ky9 insert failed for %s: %w", item.DrugName, err)
			}

			attemptLogs = append(attemptLogs, importLogEntry{
				DocNo:     po.DocNo,
				LotNumber: item.LotNumber,
				DrugName:  item.DrugName,
				Qty:       item.Qty,
			})
		}

		updateRes, err := mdb.PurchaseOrders().UpdateOne(txCtx,
			bson.M{"_id": oid, "status": "draft"},
			bson.M{"$set": bson.M{"status": "confirmed", "confirmed_at": now}},
		)
		if err != nil {
			return err
		}
		if updateRes.MatchedCount == 0 {
			return errPurchaseOrderNoLongerDraft
		}
		po.ConfirmedAt = &now
		logEntries = attemptLogs
		return nil
	}); err != nil {
		if errors.Is(err, errPurchaseOrderNoLongerDraft) {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "drug not found", http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	po.Status = "confirmed"
	for _, entry := range logEntries {
		log.Printf("IMPORT %s: lot %s | %s qty %d | ขย.9 ✓", entry.DocNo, entry.LotNumber, entry.DrugName, entry.Qty)
	}
	jsonOK(w, po)
}

// Delete removes a draft purchase order.
func (h *ImportHandler) Delete(w http.ResponseWriter, r *http.Request) {
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

	var po models.PurchaseOrder
	if err := mdb.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po); err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if po.Status != "draft" {
		jsonError(w, "cannot delete a confirmed order", http.StatusBadRequest)
		return
	}

	res, err := mdb.PurchaseOrders().DeleteOne(ctx, bson.M{"_id": oid, "status": "draft"})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if res.DeletedCount == 0 {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// buildItems converts POItemInput slice to POItem slice and computes totalCost.
// Looks up drug name from DB if DrugName is empty.
func (h *ImportHandler) buildItems(ctx context.Context, mdb *db.MongoDB, inputs []models.POItemInput) ([]models.POItem, float64, error) {
	if len(inputs) == 0 {
		return nil, 0, fmt.Errorf("items is required")
	}

	var items []models.POItem
	totalCost := 0.0
	for _, inp := range inputs {
		if inp.Qty <= 0 {
			return nil, 0, fmt.Errorf("qty must be > 0")
		}
		if inp.CostPrice < 0 {
			return nil, 0, fmt.Errorf("cost_price must be >= 0")
		}
		if inp.SellPrice != nil && *inp.SellPrice < 0 {
			return nil, 0, fmt.Errorf("sell_price must be >= 0")
		}

		oid, err := bson.ObjectIDFromHex(inp.DrugID)
		if err != nil {
			return nil, 0, err
		}

		var drug models.Drug
		if err := mdb.Drugs().FindOne(ctx, bson.M{"_id": oid}).Decode(&drug); err != nil {
			return nil, 0, err
		}

		name := drug.Name
		if inp.DrugName != "" {
			name = inp.DrugName
		}

		sellPrice := inp.SellPrice
		if sellPrice == nil {
			sellPrice = &drug.SellPrice
		}

		item := models.POItem{
			DrugID:     oid,
			DrugName:   name,
			LotNumber:  inp.LotNumber,
			ExpiryDate: inp.ExpiryDate,
			Qty:        inp.Qty,
			CostPrice:  inp.CostPrice,
			SellPrice:  sellPrice,
		}
		items = append(items, item)
		totalCost += float64(inp.Qty) * inp.CostPrice
	}
	return items, totalCost, nil
}
