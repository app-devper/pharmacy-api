package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

type ImportHandler struct{ db *db.MongoDB }

func NewImportHandler(d *db.MongoDB) *ImportHandler { return &ImportHandler{db: d} }

// List returns all purchase orders (summary only, items excluded).
func (h *ImportHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	filter := bson.M{}
	if s := r.URL.Query().Get("supplier"); s != "" {
		filter["supplier"] = s
	}

	cur, err := h.db.PurchaseOrders().Find(ctx, filter,
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var po models.PurchaseOrder
	if err := h.db.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po); err != nil {
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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Generate atomic doc_no: IMP-YYMMDD-NNN
	now := time.Now()
	today := now.Format("060102")
	counterID := "IMP-" + today
	var counter struct {
		Seq int `bson:"seq"`
	}
	err := h.db.Counters().FindOneAndUpdate(ctx,
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
		if parsed, err := time.Parse("2006-01-02", input.ReceiveDate); err == nil {
			receiveDate = parsed
		}
	}

	items, totalCost := h.buildItems(ctx, input.Items)

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

	res, err := h.db.PurchaseOrders().InsertOne(ctx, po)
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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Guard: can only update drafts
	var existing models.PurchaseOrder
	if err := h.db.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&existing); err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if existing.Status != "draft" {
		jsonError(w, "cannot edit a confirmed order", http.StatusBadRequest)
		return
	}

	receiveDate := existing.ReceiveDate
	if input.ReceiveDate != "" {
		if parsed, err := time.Parse("2006-01-02", input.ReceiveDate); err == nil {
			receiveDate = parsed
		}
	}

	items, totalCost := h.buildItems(ctx, input.Items)

	_, err = h.db.PurchaseOrders().UpdateOne(ctx, bson.M{"_id": oid},
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

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var po models.PurchaseOrder
	if err := h.db.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po); err != nil {
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
		if _, err := time.Parse("2006-01-02", item.ExpiryDate); err != nil {
			jsonError(w, fmt.Sprintf("รายการที่ %d: วันหมดอายุไม่ถูกต้อง", i+1), http.StatusBadRequest)
			return
		}
	}

	// Process each item
	now := time.Now()
	receiveDate := po.ReceiveDate.Format("2006-01-02")

	for _, item := range po.Items {
		expiry, _ := time.Parse("2006-01-02", item.ExpiryDate)
		costPrice := item.CostPrice

		// 1. Create DrugLot
		lot := models.DrugLot{
			DrugID:     item.DrugID,
			LotNumber:  item.LotNumber,
			ExpiryDate: expiry,
			ImportDate: po.ReceiveDate,
			CostPrice:  &costPrice,
			SellPrice:  item.SellPrice,
			Quantity:   item.Qty,
			Remaining:  item.Qty,
			CreatedAt:  now,
		}
		if _, err := h.db.DrugLots().InsertOne(ctx, lot); err != nil {
			jsonError(w, "lot insert failed for "+item.DrugName+": "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 2. Increment drug stock
		if _, err := h.db.Drugs().UpdateOne(ctx,
			bson.M{"_id": item.DrugID},
			bson.M{"$inc": bson.M{"stock": item.Qty}},
		); err != nil {
			jsonError(w, "stock update failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 3. Auto-create ขย.9 — look up drug for reg_no and unit
		var drug models.Drug
		h.db.Drugs().FindOne(ctx, bson.M{"_id": item.DrugID}).Decode(&drug)
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
		if _, err := h.db.Ky9().InsertOne(ctx, ky9); err != nil {
			// non-fatal — log แต่ไม่หยุด
			log.Printf("IMPORT %s: ky9 insert warning for %s: %v", po.DocNo, item.DrugName, err)
		}

		log.Printf("IMPORT %s: lot %s | %s qty %d | ขย.9 ✓", po.DocNo, item.LotNumber, item.DrugName, item.Qty)
	}

	// Mark as confirmed
	_, err = h.db.PurchaseOrders().UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$set": bson.M{"status": "confirmed", "confirmed_at": now}},
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	po.Status = "confirmed"
	po.ConfirmedAt = &now
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var po models.PurchaseOrder
	if err := h.db.PurchaseOrders().FindOne(ctx, bson.M{"_id": oid}).Decode(&po); err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	if po.Status != "draft" {
		jsonError(w, "cannot delete a confirmed order", http.StatusBadRequest)
		return
	}

	h.db.PurchaseOrders().DeleteOne(ctx, bson.M{"_id": oid})
	jsonOK(w, map[string]bool{"ok": true})
}

// buildItems converts POItemInput slice to POItem slice and computes totalCost.
// Looks up drug name from DB if DrugName is empty.
func (h *ImportHandler) buildItems(ctx context.Context, inputs []models.POItemInput) ([]models.POItem, float64) {
	var items []models.POItem
	totalCost := 0.0
	for _, inp := range inputs {
		oid, err := bson.ObjectIDFromHex(inp.DrugID)
		if err != nil {
			continue
		}
		name := inp.DrugName
		if name == "" {
			var drug models.Drug
			h.db.Drugs().FindOne(ctx, bson.M{"_id": oid}).Decode(&drug)
			name = drug.Name
		}
		item := models.POItem{
			DrugID:     oid,
			DrugName:   name,
			LotNumber:  inp.LotNumber,
			ExpiryDate: inp.ExpiryDate,
			Qty:        inp.Qty,
			CostPrice:  inp.CostPrice,
			SellPrice:  inp.SellPrice,
		}
		items = append(items, item)
		totalCost += float64(inp.Qty) * inp.CostPrice
	}
	if items == nil {
		items = []models.POItem{}
	}
	return items, totalCost
}
