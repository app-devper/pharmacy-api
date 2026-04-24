package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type StockCountHandler struct{ dbm *db.Manager }

func NewStockCountHandler(d *db.Manager) *StockCountHandler {
	return &StockCountHandler{dbm: d}
}

func (h *StockCountHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := int64(20)
	if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64); err == nil && v > 0 && v <= 100 {
		limit = v
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cur, err := mdb.StockCounts().Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit),
	)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	var counts []models.StockCount
	if err := cur.All(ctx, &counts); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if counts == nil {
		counts = []models.StockCount{}
	}
	jsonOK(w, counts)
}

func (h *StockCountHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.StockCountInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(input.Items) == 0 {
		jsonError(w, "items is required", http.StatusBadRequest)
		return
	}
	if len(input.Items) > 1000 {
		jsonError(w, "ไม่เกิน 1,000 รายการต่อรอบตรวจนับ", http.StatusBadRequest)
		return
	}

	seen := make(map[bson.ObjectID]struct{}, len(input.Items))
	parsed := make([]struct {
		id      bson.ObjectID
		counted int
	}, 0, len(input.Items))
	for _, item := range input.Items {
		if item.Counted < 0 {
			jsonError(w, "counted must be >= 0", http.StatusBadRequest)
			return
		}
		oid, err := bson.ObjectIDFromHex(item.DrugID)
		if err != nil {
			jsonError(w, "invalid drug id", http.StatusBadRequest)
			return
		}
		if _, dup := seen[oid]; dup {
			jsonError(w, "duplicate drug id", http.StatusBadRequest)
			return
		}
		seen[oid] = struct{}{}
		parsed = append(parsed, struct {
			id      bson.ObjectID
			counted int
		}{id: oid, counted: item.Counted})
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tz := loadTimezone(ctx, mdb)

	var count models.StockCount
	if err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		now := time.Now().In(tz)
		countNo, err := h.nextStockCountNo(txCtx, mdb, now)
		if err != nil {
			return err
		}

		items := make([]models.StockCountItem, 0, len(parsed))
		for _, item := range parsed {
			var drug models.Drug
			if err := mdb.Drugs().FindOne(txCtx, bson.M{"_id": item.id}).Decode(&drug); err != nil {
				return err
			}

			delta := item.counted - drug.Stock
			countItem := models.StockCountItem{
				DrugID:      item.id,
				DrugName:    drug.Name,
				Unit:        drug.Unit,
				SystemStock: drug.Stock,
				Counted:     item.counted,
				Delta:       delta,
			}
			items = append(items, countItem)
			if delta == 0 {
				continue
			}

			updateRes, err := mdb.Drugs().UpdateOne(txCtx,
				bson.M{"_id": item.id},
				bson.M{"$set": bson.M{"stock": item.counted}},
			)
			if err != nil {
				return err
			}
			if updateRes.MatchedCount == 0 {
				return mongo.ErrNoDocuments
			}
			if delta > 0 {
				if _, err := reconcileOversoldFromAdjustment(txCtx, mdb, item.id, delta, models.AdjustmentReasons[0]); err != nil {
					return err
				}
			}

			adj := models.StockAdjustment{
				DrugID:    item.id,
				DrugName:  drug.Name,
				Delta:     delta,
				Before:    drug.Stock,
				After:     item.counted,
				Reason:    models.AdjustmentReasons[0],
				Note:      strings.TrimSpace(fmt.Sprintf("%s %s", countNo, input.Note)),
				CreatedAt: now,
			}
			if _, err := mdb.StockAdjustments().InsertOne(txCtx, adj); err != nil {
				return err
			}
		}

		count = models.StockCount{
			CountNo:   countNo,
			Note:      strings.TrimSpace(input.Note),
			Items:     items,
			CreatedAt: now,
		}
		res, err := mdb.StockCounts().InsertOne(txCtx, count)
		if err != nil {
			return err
		}
		count.ID = res.InsertedID.(bson.ObjectID)
		return nil
	}); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			jsonError(w, "drug not found", http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonOK(w, count)
}

func (h *StockCountHandler) nextStockCountNo(ctx context.Context, mdb *db.MongoDB, now time.Time) (string, error) {
	today := now.Format("060102")
	counterID := "SC-" + today
	var counter struct {
		Seq int `bson:"seq"`
	}
	err := mdb.Counters().FindOneAndUpdate(ctx,
		bson.M{"_id": counterID},
		bson.M{"$inc": bson.M{"seq": 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&counter)
	if err != nil {
		return "", fmt.Errorf("stock count number error: %w", err)
	}
	return fmt.Sprintf("SC-%s-%03d", today, counter.Seq), nil
}
