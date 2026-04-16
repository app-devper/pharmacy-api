package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

type SaleHandler struct{ db *db.MongoDB }

func NewSaleHandler(d *db.MongoDB) *SaleHandler { return &SaleHandler{db: d} }

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
		filter["$or"] = bson.A{
			bson.M{"bill_no": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"customer_name": bson.M{"$regex": search, "$options": "i"}},
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.Sales().Find(ctx, filter,
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

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Generate atomic bill number
	now := time.Now()
	today := now.Format("060102")
	counterID := "INV-" + today
	var counter struct {
		Seq int `bson:"seq"`
	}
	err := h.db.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": counterID},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		jsonError(w, "bill number error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	billNo := fmt.Sprintf("INV-%s-%03d", today, counter.Seq)

	// Calculate subtotal then apply discount
	var subtotal float64
	for _, item := range input.Items {
		subtotal += item.Price * float64(item.Qty)
	}
	discount := math.Max(0, math.Min(input.Discount, subtotal))
	total := subtotal - discount

	received := input.Received
	if received == 0 {
		received = total
	}
	change := math.Max(0, received-total)

	// Resolve customer
	var custOID *bson.ObjectID
	var custName string
	if input.CustomerID != nil && *input.CustomerID != "" {
		oid, err := bson.ObjectIDFromHex(*input.CustomerID)
		if err == nil {
			custOID = &oid
			var cust models.Customer
			h.db.Customers().FindOne(ctx, bson.M{"_id": oid}).Decode(&cust)
			custName = cust.Name
		}
	}

	// Insert sale
	sale := models.Sale{
		BillNo:       billNo,
		CustomerID:   custOID,
		CustomerName: custName,
		Discount:     discount,
		Total:        total,
		Received:     received,
		Change:       change,
		SoldAt:       now,
	}
	res, err := h.db.Sales().InsertOne(ctx, sale)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saleOID := res.InsertedID.(bson.ObjectID)

	// Insert sale items, FEFO lot deduction, and update stock aggregate
	for _, item := range input.Items {
		drugOID, err := bson.ObjectIDFromHex(item.DrugID)
		if err != nil {
			continue
		}
		var drug models.Drug
		h.db.Drugs().FindOne(ctx, bson.M{"_id": drugOID}).Decode(&drug)

		si := models.SaleItem{
			SaleID:   saleOID,
			DrugID:   drugOID,
			DrugName: drug.Name,
			Qty:      item.Qty,
			Price:    item.Price,
			Subtotal: item.Price * float64(item.Qty),
		}
		h.db.SaleItems().InsertOne(ctx, si)

		// FEFO: deduct from lots sorted by expiry_date ASC (earliest first)
		lotCur, lotErr := h.db.DrugLots().Find(ctx,
			bson.M{"drug_id": drugOID, "remaining": bson.M{"$gt": 0}},
			options.Find().SetSort(bson.D{{Key: "expiry_date", Value: 1}}),
		)
		if lotErr == nil {
			var lots []models.DrugLot
			lotCur.All(ctx, &lots)
			lotCur.Close(ctx)
			need := item.Qty
			for _, lot := range lots {
				if need <= 0 {
					break
				}
				deduct := lot.Remaining
				if deduct > need {
					deduct = need
				}
				h.db.DrugLots().UpdateOne(ctx,
					bson.M{"_id": lot.ID},
					bson.M{"$inc": bson.M{"remaining": -deduct}},
				)
				need -= deduct
			}
		}

		// Decrement drug stock aggregate
		h.db.Drugs().UpdateOne(ctx,
			bson.M{"_id": drugOID},
			bson.M{"$inc": bson.M{"stock": -item.Qty}},
		)
	}

	// Update customer stats
	if custOID != nil {
		h.db.Customers().UpdateOne(ctx,
			bson.M{"_id": custOID},
			bson.M{
				"$inc": bson.M{"total_spent": total},
				"$set": bson.M{"last_visit": now},
			},
		)
	}

	jsonOK(w, models.SaleResponse{BillNo: billNo, Discount: discount, Total: total, Change: change})
}

func (h *SaleHandler) Items(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := h.db.SaleItems().Find(ctx, bson.M{"sale_id": oid})
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
	json.NewDecoder(r.Body).Decode(&body)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Fetch sale
	var sale models.Sale
	if err := h.db.Sales().FindOne(ctx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		jsonError(w, "sale not found", http.StatusNotFound)
		return
	}
	if sale.Voided {
		jsonError(w, "sale already voided", http.StatusConflict)
		return
	}

	// Mark as voided
	now := time.Now()
	_, err = h.db.Sales().UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{
			"voided":      true,
			"void_reason": body.Reason,
			"voided_at":   now,
		}},
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Restore stock and lot remaining for each item
	itemCur, err := h.db.SaleItems().Find(ctx, bson.M{"sale_id": oid})
	if err == nil {
		var items []models.SaleItem
		itemCur.All(ctx, &items)
		itemCur.Close(ctx)
		for _, item := range items {
			h.db.Drugs().UpdateOne(ctx,
				bson.M{"_id": item.DrugID},
				bson.M{"$inc": bson.M{"stock": item.Qty}},
			)

			// Reverse-FEFO: restore lot.remaining (latest-expiring first)
			lotCur, lotErr := h.db.DrugLots().Find(ctx,
				bson.M{"drug_id": item.DrugID},
				options.Find().SetSort(bson.D{{Key: "expiry_date", Value: -1}}),
			)
			if lotErr == nil {
				var lots []models.DrugLot
				lotCur.All(ctx, &lots)
				lotCur.Close(ctx)
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
					h.db.DrugLots().UpdateOne(ctx,
						bson.M{"_id": lot.ID},
						bson.M{"$inc": bson.M{"remaining": restore}},
					)
					need -= restore
				}
			}
		}
	}

	// Reverse customer spend
	if sale.CustomerID != nil {
		h.db.Customers().UpdateOne(ctx,
			bson.M{"_id": sale.CustomerID},
			bson.M{"$inc": bson.M{"total_spent": -sale.Total}},
		)
	}

	jsonOK(w, map[string]bool{"ok": true})
}
