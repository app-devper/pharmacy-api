package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type ReturnHandler struct{ dbm *db.Manager }

func NewReturnHandler(d *db.Manager) *ReturnHandler { return &ReturnHandler{dbm: d} }

// Create processes a partial drug return linked to an existing sale.
// POST /api/sales/{id}/return
func (h *ReturnHandler) Create(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid id", http.StatusBadRequest)
		return
	}

	var input models.DrugReturnInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Reason == "" {
		jsonError(w, "reason is required", http.StatusBadRequest)
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

	var sale models.Sale
	if err := mdb.Sales().FindOne(ctx, bson.M{"_id": oid}).Decode(&sale); err != nil {
		jsonError(w, "sale not found", http.StatusNotFound)
		return
	}
	if sale.Voided {
		jsonError(w, "ไม่สามารถคืนยาจากบิลที่ยกเลิกแล้ว", http.StatusBadRequest)
		return
	}

	itemCur, err := mdb.SaleItems().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var saleItems []models.SaleItem
	if err := itemCur.All(ctx, &saleItems); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	itemCur.Close(ctx)

	saleItemMap := make(map[string]models.SaleItem, len(saleItems))
	for _, si := range saleItems {
		saleItemMap[si.ID.Hex()] = si
	}

	retCur, err := mdb.DrugReturns().Find(ctx, bson.M{"sale_id": oid})
	if err != nil {
		jsonError(w, "failed to load existing returns: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var existingReturns []models.DrugReturn
	if err := retCur.All(ctx, &existingReturns); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	retCur.Close(ctx)

	alreadyReturned := make(map[string]int)
	for _, ret := range existingReturns {
		for _, ri := range ret.Items {
			alreadyReturned[ri.SaleItemID.Hex()] += ri.Qty
		}
	}

	for _, inp := range input.Items {
		si, ok := saleItemMap[inp.SaleItemID]
		if !ok {
			jsonError(w, fmt.Sprintf("sale item %s not found", inp.SaleItemID), http.StatusBadRequest)
			return
		}
		if inp.Qty <= 0 {
			jsonError(w, "qty must be > 0", http.StatusBadRequest)
			return
		}
		if inp.Qty+alreadyReturned[inp.SaleItemID] > si.Qty {
			jsonError(w, fmt.Sprintf("คืนเกินจำนวนที่ขาย: %s (ขายไป %d, คืนแล้ว %d)", si.DrugName, si.Qty, alreadyReturned[inp.SaleItemID]), http.StatusBadRequest)
			return
		}
	}

	now := time.Now()
	today := now.Format("060102")
	counterID := "RET-" + today
	var counter struct {
		Seq int `bson:"seq"`
	}
	err = mdb.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": counterID},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		jsonError(w, "return number error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	returnNo := fmt.Sprintf("RET-%s-%03d", today, counter.Seq)

	var returnItems []models.ReturnItem
	var refund float64

	for _, inp := range input.Items {
		si := saleItemMap[inp.SaleItemID]
		siOID, _ := bson.ObjectIDFromHex(inp.SaleItemID)

		subtotal := float64(inp.Qty) * si.Price
		refund += subtotal

		returnItems = append(returnItems, models.ReturnItem{
			SaleItemID: siOID,
			DrugID:     si.DrugID,
			DrugName:   si.DrugName,
			Qty:        inp.Qty,
			Price:      si.Price,
			Subtotal:   subtotal,
		})

		mdb.Drugs().UpdateOne(ctx,
			bson.M{"_id": si.DrugID},
			bson.M{"$inc": bson.M{"stock": inp.Qty}},
		)

		// Reverse-FEFO: restore lot.remaining (latest-expiring first)
		lotCur, lotErr := mdb.DrugLots().Find(ctx,
			bson.M{"drug_id": si.DrugID},
			options.Find().SetSort(bson.D{{Key: "expiry_date", Value: -1}}),
		)
		if lotErr == nil {
			var lots []models.DrugLot
			lotCur.All(ctx, &lots)
			lotCur.Close(ctx)
			need := inp.Qty
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
				mdb.DrugLots().UpdateOne(ctx,
					bson.M{"_id": lot.ID},
					bson.M{"$inc": bson.M{"remaining": restore}},
				)
				need -= restore
			}
		}
	}

	ret := models.DrugReturn{
		ReturnNo:     returnNo,
		SaleID:       oid,
		BillNo:       sale.BillNo,
		CustomerID:   sale.CustomerID,
		CustomerName: sale.CustomerName,
		Items:        returnItems,
		Refund:       refund,
		Reason:       input.Reason,
		ReturnedAt:   now,
	}
	res, err := mdb.DrugReturns().InsertOne(ctx, ret)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ret.ID = res.InsertedID.(bson.ObjectID)

	// Reverse customer spend
	if sale.CustomerID != nil {
		mdb.Customers().UpdateOne(ctx,
			bson.M{"_id": sale.CustomerID},
			bson.M{"$inc": bson.M{"total_spent": -refund}},
		)
	}

	jsonOK(w, ret)
}

// List returns all drug returns for a sale.
// GET /api/sales/{id}/returns
func (h *ReturnHandler) List(w http.ResponseWriter, r *http.Request) {
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

	cur, err := mdb.DrugReturns().Find(ctx,
		bson.M{"sale_id": oid},
		options.Find().SetSort(bson.D{{Key: "returned_at", Value: -1}}),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var returns []models.DrugReturn
	if err := cur.All(ctx, &returns); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if returns == nil {
		returns = []models.DrugReturn{}
	}
	jsonOK(w, returns)
}
