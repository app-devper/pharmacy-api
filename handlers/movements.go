package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
)

// MovementEntry is one unified stock-movement record returned by GET /api/movements.
type MovementEntry struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`      // import|sale|return|adjustment|writeoff
	DrugID    string    `json:"drug_id"`
	DrugName  string    `json:"drug_name"`
	Delta     int       `json:"delta"`     // positive = stock in, negative = stock out
	Reference string    `json:"reference"` // bill_no / lot_number / return_no / reason
	Note      string    `json:"note"`
	At        time.Time `json:"at"`
}

type MovementsHandler struct{ dbm *db.Manager }

func NewMovementsHandler(d *db.Manager) *MovementsHandler { return &MovementsHandler{dbm: d} }

// List handles GET /api/movements
// Query params: from, to (YYYY-MM-DD), drug_name, types (comma-sep), limit, offset
func (h *MovementsHandler) List(w http.ResponseWriter, r *http.Request) {
	d, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	q := r.URL.Query()

	// --- date range ---
	now := time.Now()
	from := now.AddDate(0, 0, -30)
	to := now.Add(24 * time.Hour)
	if s := q.Get("from"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			from = t
		}
	}
	if s := q.Get("to"); s != "" {
		if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			to = t.Add(24 * time.Hour)
		}
	}

	// --- type filter ---
	typeSet := map[string]bool{
		"import": true, "sale": true, "return": true,
		"adjustment": true, "writeoff": true,
	}
	if tp := q.Get("types"); tp != "" {
		for k := range typeSet {
			typeSet[k] = false
		}
		for _, t := range strings.Split(tp, ",") {
			typeSet[strings.TrimSpace(t)] = true
		}
	}

	// --- drug_name filter (regex) ---
	drugName := strings.TrimSpace(q.Get("drug_name"))

	// --- pagination ---
	limit := 50
	offset := 0
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if s := q.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var mu sync.Mutex
	var all []MovementEntry
	var wg sync.WaitGroup

	addEntries := func(entries []MovementEntry) {
		mu.Lock()
		all = append(all, entries...)
		mu.Unlock()
	}

	if typeSet["import"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addEntries(fetchImports(ctx, d, from, to, drugName))
		}()
	}
	if typeSet["sale"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addEntries(fetchSales(ctx, d, from, to, drugName))
		}()
	}
	if typeSet["return"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addEntries(fetchReturns(ctx, d, from, to, drugName))
		}()
	}
	if typeSet["adjustment"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addEntries(fetchAdjustments(ctx, d, from, to, drugName))
		}()
	}
	if typeSet["writeoff"] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addEntries(fetchWriteoffs(ctx, d, from, to, drugName))
		}()
	}

	wg.Wait()

	// sort by At descending
	sort.Slice(all, func(i, j int) bool {
		return all[i].At.After(all[j].At)
	})

	total := len(all)

	// paginate
	end := offset + limit
	if offset >= len(all) {
		all = []MovementEntry{}
	} else {
		if end > len(all) {
			end = len(all)
		}
		all = all[offset:end]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total": total,
		"items": all,
	})
}

// ─── import ───────────────────────────────────────────────────────────────────

func fetchImports(ctx context.Context, d *db.MongoDB, from, to time.Time, drugName string) []MovementEntry {
	matchStage := bson.D{
		{Key: "import_date", Value: bson.M{"$gte": from, "$lt": to}},
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: matchStage}},
		{{Key: "$lookup", Value: bson.M{
			"from": "drugs", "localField": "drug_id",
			"foreignField": "_id", "as": "drug",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$drug", "preserveNullAndEmptyArrays": true}}},
	}
	if drugName != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{
			"drug.name": bson.M{"$regex": regexp.QuoteMeta(drugName), "$options": "i"},
		}}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$limit", Value: 2000}})

	cur, err := d.DrugLots().Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var results []struct {
		ID         bson.ObjectID `bson:"_id"`
		LotNumber  string        `bson:"lot_number"`
		Quantity   int           `bson:"quantity"`
		ImportDate time.Time     `bson:"import_date"`
		Drug       struct {
			ID   bson.ObjectID `bson:"_id"`
			Name string        `bson:"name"`
		} `bson:"drug"`
	}
	cur.All(ctx, &results)

	entries := make([]MovementEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, MovementEntry{
			ID:        r.ID.Hex(),
			Type:      "import",
			DrugID:    r.Drug.ID.Hex(),
			DrugName:  r.Drug.Name,
			Delta:     r.Quantity,
			Reference: r.LotNumber,
			At:        r.ImportDate,
		})
	}
	return entries
}

// ─── sale ─────────────────────────────────────────────────────────────────────

func fetchSales(ctx context.Context, d *db.MongoDB, from, to time.Time, drugName string) []MovementEntry {
	pipeline := mongo.Pipeline{
		// join with sales to get sold_at, bill_no, voided
		{{Key: "$lookup", Value: bson.M{
			"from": "sales", "localField": "sale_id",
			"foreignField": "_id", "as": "sale",
		}}},
		{{Key: "$unwind", Value: "$sale"}},
		{{Key: "$match", Value: bson.M{
			"sale.sold_at": bson.M{"$gte": from, "$lt": to},
			"sale.voided":  bson.M{"$ne": true},
		}}},
		// join with drugs for drug_name
		{{Key: "$lookup", Value: bson.M{
			"from": "drugs", "localField": "drug_id",
			"foreignField": "_id", "as": "drug",
		}}},
		{{Key: "$unwind", Value: bson.M{"path": "$drug", "preserveNullAndEmptyArrays": true}}},
	}
	if drugName != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{
			"drug.name": bson.M{"$regex": regexp.QuoteMeta(drugName), "$options": "i"},
		}}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$limit", Value: 5000}})

	cur, err := d.SaleItems().Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var results []struct {
		ID   bson.ObjectID `bson:"_id"`
		Qty  int           `bson:"qty"`
		Sale struct {
			BillNo string    `bson:"bill_no"`
			SoldAt time.Time `bson:"sold_at"`
		} `bson:"sale"`
		Drug struct {
			ID   bson.ObjectID `bson:"_id"`
			Name string        `bson:"name"`
		} `bson:"drug"`
	}
	cur.All(ctx, &results)

	entries := make([]MovementEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, MovementEntry{
			ID:        r.ID.Hex(),
			Type:      "sale",
			DrugID:    r.Drug.ID.Hex(),
			DrugName:  r.Drug.Name,
			Delta:     -r.Qty,
			Reference: r.Sale.BillNo,
			At:        r.Sale.SoldAt,
		})
	}
	return entries
}

// ─── return ───────────────────────────────────────────────────────────────────

func fetchReturns(ctx context.Context, d *db.MongoDB, from, to time.Time, drugName string) []MovementEntry {
	matchName := bson.M{}
	if drugName != "" {
		matchName = bson.M{
			"items.drug_name": bson.M{"$regex": regexp.QuoteMeta(drugName), "$options": "i"},
		}
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"returned_at": bson.M{"$gte": from, "$lt": to},
		}}},
		{{Key: "$unwind", Value: "$items"}},
	}
	if drugName != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: matchName}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$limit", Value: 2000}})

	cur, err := d.DrugReturns().Aggregate(ctx, pipeline)
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var results []struct {
		ID         bson.ObjectID `bson:"_id"`
		ReturnNo   string        `bson:"return_no"`
		ReturnedAt time.Time     `bson:"returned_at"`
		Items      struct {
			DrugID   bson.ObjectID `bson:"drug_id"`
			DrugName string        `bson:"drug_name"`
			Qty      int           `bson:"qty"`
		} `bson:"items"`
	}
	cur.All(ctx, &results)

	entries := make([]MovementEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, MovementEntry{
			ID:        r.ID.Hex(),
			Type:      "return",
			DrugID:    r.Items.DrugID.Hex(),
			DrugName:  r.Items.DrugName,
			Delta:     r.Items.Qty,
			Reference: r.ReturnNo,
			At:        r.ReturnedAt,
		})
	}
	return entries
}

// ─── adjustment ───────────────────────────────────────────────────────────────

func fetchAdjustments(ctx context.Context, d *db.MongoDB, from, to time.Time, drugName string) []MovementEntry {
	filter := bson.M{
		"created_at": bson.M{"$gte": from, "$lt": to},
	}
	if drugName != "" {
		filter["drug_name"] = bson.M{"$regex": regexp.QuoteMeta(drugName), "$options": "i"}
	}

	cur, err := d.StockAdjustments().Find(ctx, filter)
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var results []struct {
		ID        bson.ObjectID `bson:"_id"`
		DrugID    bson.ObjectID `bson:"drug_id"`
		DrugName  string        `bson:"drug_name"`
		Delta     int           `bson:"delta"`
		Reason    string        `bson:"reason"`
		Note      string        `bson:"note"`
		CreatedAt time.Time     `bson:"created_at"`
	}
	cur.All(ctx, &results)

	entries := make([]MovementEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, MovementEntry{
			ID:        r.ID.Hex(),
			Type:      "adjustment",
			DrugID:    r.DrugID.Hex(),
			DrugName:  r.DrugName,
			Delta:     r.Delta,
			Reference: r.Reason,
			Note:      r.Note,
			At:        r.CreatedAt,
		})
	}
	return entries
}

// ─── writeoff ─────────────────────────────────────────────────────────────────

func fetchWriteoffs(ctx context.Context, d *db.MongoDB, from, to time.Time, drugName string) []MovementEntry {
	filter := bson.M{
		"created_at": bson.M{"$gte": from, "$lt": to},
	}
	if drugName != "" {
		filter["drug_name"] = bson.M{"$regex": regexp.QuoteMeta(drugName), "$options": "i"}
	}

	cur, err := d.LotWriteoffs().Find(ctx, filter)
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var results []struct {
		ID        bson.ObjectID `bson:"_id"`
		DrugID    bson.ObjectID `bson:"drug_id"`
		DrugName  string        `bson:"drug_name"`
		LotNumber string        `bson:"lot_number"`
		Qty       int           `bson:"qty"`
		CreatedAt time.Time     `bson:"created_at"`
	}
	cur.All(ctx, &results)

	entries := make([]MovementEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, MovementEntry{
			ID:        r.ID.Hex(),
			Type:      "writeoff",
			DrugID:    r.DrugID.Hex(),
			DrugName:  r.DrugName,
			Delta:     -r.Qty,
			Reference: r.LotNumber,
			At:        r.CreatedAt,
		})
	}
	return entries
}
