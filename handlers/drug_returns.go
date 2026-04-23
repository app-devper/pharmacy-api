package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type ReturnHandler struct{ dbm *db.Manager }

func NewReturnHandler(d *db.Manager) *ReturnHandler { return &ReturnHandler{dbm: d} }

// Create processes a partial drug return linked to an existing sale.
// POST /api/pharmacy/v1/sales/{id}/return
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
	tz := loadTimezone(ctx, mdb)

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

	var ret models.DrugReturn
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		currentReturned := make(map[string]int)
		retCur, err := mdb.DrugReturns().Find(txCtx, bson.M{"sale_id": oid})
		if err != nil {
			return fmt.Errorf("failed to load existing returns: %w", err)
		}
		var txReturns []models.DrugReturn
		if err := retCur.All(txCtx, &txReturns); err != nil {
			retCur.Close(txCtx)
			return err
		}
		retCur.Close(txCtx)
		for _, ret := range txReturns {
			for _, ri := range ret.Items {
				currentReturned[ri.SaleItemID.Hex()] += ri.Qty
			}
		}

		for _, inp := range input.Items {
			si := saleItemMap[inp.SaleItemID]
			if inp.Qty+currentReturned[inp.SaleItemID] > si.Qty {
				return fmt.Errorf("คืนเกินจำนวนที่ขาย: %s (ขายไป %d, คืนแล้ว %d)", si.DrugName, si.Qty, currentReturned[inp.SaleItemID])
			}
			// Only units backed by a real lot can be returned — the unreconciled
			// oversold portion has no lot to give back to, and the synthetic
			// (adjustment-reconciled) portion was absorbed from bulk stock
			// rather than a specific lot. Cap returnable at the sum of real
			// LotSplits (non-zero LotID).
			realLotQty := 0
			for _, sp := range si.LotSplits {
				if !sp.LotID.IsZero() {
					realLotQty += sp.Qty
				}
			}
			if realLotQty < si.Qty {
				if inp.Qty+currentReturned[inp.SaleItemID] > realLotQty {
					pending := si.Qty - realLotQty
					return fmt.Errorf("ยา %s: คืนได้สูงสุด %d (มี %d หน่วยยังไม่ผูกกับล็อตจริง)", si.DrugName, realLotQty, pending)
				}
			}
		}

		now := time.Now()
		// Counter keyed by local calendar day so same-day returns share one seq
		// and the RET-YYMMDD prefix matches the pharmacy's local date.
		today := now.In(tz).Format("060102")
		counterID := "RET-" + today
		var counter struct {
			Seq int `bson:"seq"`
		}
		if err := mdb.Counters().FindOneAndUpdate(txCtx,
			bson.M{"_id": counterID},
			bson.M{"$inc": bson.M{"seq": 1}},
			options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
		).Decode(&counter); err != nil {
			return fmt.Errorf("return number error: %w", err)
		}
		returnNo := fmt.Sprintf("RET-%s-%03d", today, counter.Seq)

		returnItems := make([]models.ReturnItem, 0, len(input.Items))
		refund := 0.0

		for _, inp := range input.Items {
			si := saleItemMap[inp.SaleItemID]
			siOID, _ := bson.ObjectIDFromHex(inp.SaleItemID)

			subtotal := float64(inp.Qty) * si.Price
			costSubtotal := 0.0
			if si.Qty > 0 {
				costSubtotal = (si.CostSubtotal / float64(si.Qty)) * float64(inp.Qty)
			}
			refund += subtotal

			returnItems = append(returnItems, models.ReturnItem{
				SaleItemID:   siOID,
				DrugID:       si.DrugID,
				DrugName:     si.DrugName,
				Qty:          inp.Qty,
				Price:        si.Price,
				Subtotal:     subtotal,
				CostSubtotal: costSubtotal,
			})

			updateRes, err := mdb.Drugs().UpdateOne(txCtx,
				bson.M{"_id": si.DrugID},
				bson.M{"$inc": bson.M{"stock": inp.Qty}},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}

			if err := h.restoreReturnItemLots(txCtx, mdb, si, inp.Qty); err != nil {
				return err
			}
		}

		ret = models.DrugReturn{
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
		res, err := mdb.DrugReturns().InsertOne(txCtx, ret)
		if err != nil {
			return err
		}
		ret.ID = res.InsertedID.(bson.ObjectID)

		if sale.CustomerID != nil {
			updateRes, err := mdb.Customers().UpdateOne(txCtx,
				bson.M{"_id": sale.CustomerID},
				bson.M{"$inc": bson.M{"total_spent": -refund}},
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
		switch {
		case errors.Is(err, mongo.ErrNoDocuments):
			jsonError(w, "referenced document not found", http.StatusBadRequest)
		default:
			jsonError(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	jsonOK(w, ret)
}

// List returns all drug returns for a sale.
// GET /api/pharmacy/v1/sales/{id}/returns
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

func (h *ReturnHandler) restoreReturnItemLots(ctx context.Context, mdb *db.MongoDB, item models.SaleItem, qty int) error {
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
	if len(lots) == 0 {
		return nil
	}

	need := qty
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

	if need > 0 {
		return fmt.Errorf("failed to fully restore lot inventory for %s", item.DrugName)
	}
	return nil
}
