package inventory

import (
	"context"
	"regexp"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// Stock movements (ADR-0010, KMP ADR-0003) are a view of the records behind
// every stock change, not a ledger: one entry per change, so a period's
// entries for a drug add up to how its stock changed.
//
//   - import: a lot received or opened with a drug. A lot created by a stock
//     increase is not an import; its adjustment is the movement.
//   - sale: a sale line, unless the sale was voided (the void gives it back).
//   - return, adjustment (including stock counts), writeoff.
//
// Deleting a lot nothing was sold from undoes its receipt: the lot drops out
// of the view, and its deletion shows only the units that did not come from
// that receipt (e.g. an increase into the lot).

// Movement kinds.
const (
	MoveImport     = "import"
	MoveSale       = "sale"
	MoveReturn     = "return"
	MoveAdjustment = "adjustment"
	MoveWriteoff   = "writeoff"
)

// AllMoves is every movement kind.
var AllMoves = []string{MoveImport, MoveSale, MoveReturn, MoveAdjustment, MoveWriteoff}

// Movement is one stock change.
type Movement struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	DrugID    string    `json:"drug_id"`
	DrugName  string    `json:"drug_name"`
	Delta     int       `json:"delta"`     // positive = stock in, negative = stock out
	Reference string    `json:"reference"` // bill_no / lot_number / return_no / reason
	Note      string    `json:"note"`
	At        time.Time `json:"at"`
}

// MovementQuery selects movements in [From, To) of the given kinds, for drugs
// whose name contains DrugName (case-insensitive) when it is set.
type MovementQuery struct {
	From, To time.Time
	Kinds    []string
	DrugName string
}

// legacyLotWindow: lots created before origins were recorded count as made by
// an adjustment when one referencing them was written this close in time
// (the same transaction).
const legacyLotWindow = time.Minute

// Movements lists the query's movements, newest first.
func Movements(ctx context.Context, mdb *db.MongoDB, q MovementQuery) ([]Movement, error) {
	fetch := map[string]func(context.Context, *db.MongoDB, MovementQuery) ([]Movement, error){
		MoveImport: imports, MoveSale: saleMoves, MoveReturn: returnMoves,
		MoveAdjustment: adjustmentMoves, MoveWriteoff: writeoffMoves,
	}
	var all []Movement
	for _, kind := range q.Kinds {
		f, ok := fetch[kind]
		if !ok {
			continue
		}
		moves, err := f(ctx, mdb, q)
		if err != nil {
			return nil, err
		}
		all = append(all, moves...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	return all, nil
}

func inPeriod(q MovementQuery) bson.M { return bson.M{"$gte": q.From, "$lt": q.To} }

func nameLike(name string) bson.M {
	return bson.M{"$regex": regexp.QuoteMeta(name), "$options": "i"}
}

func imports(ctx context.Context, mdb *db.MongoDB, q MovementQuery) ([]Movement, error) {
	filter := bson.M{"created_at": inPeriod(q), "origin": bson.M{"$ne": models.LotAdjustment}}
	if q.DrugName != "" {
		filter["drug_name"] = nameLike(q.DrugName)
	}
	var lots []models.DrugLot
	if err := findAll(ctx, mdb.DrugLots(), filter, &lots); err != nil {
		return nil, err
	}
	madeByAdjustment, err := legacyAdjustmentLots(ctx, mdb, lots)
	if err != nil {
		return nil, err
	}
	out := make([]Movement, 0, len(lots))
	for _, l := range lots {
		if madeByAdjustment[l.ID] {
			continue
		}
		out = append(out, Movement{
			ID: l.ID.Hex(), Type: MoveImport, DrugID: l.DrugID.Hex(), DrugName: l.DrugName,
			Delta: l.Quantity, Reference: l.LotNumber, At: l.CreatedAt,
		})
	}
	return out, nil
}

// legacyAdjustmentLots finds, among lots with no recorded origin, those an
// increase created: an adjustment referencing the lot was written with it.
func legacyAdjustmentLots(ctx context.Context, mdb *db.MongoDB, lots []models.DrugLot) (map[bson.ObjectID]bool, error) {
	created := map[bson.ObjectID]time.Time{}
	ids := bson.A{}
	for _, l := range lots {
		if l.Origin == "" {
			created[l.ID] = l.CreatedAt
			ids = append(ids, l.ID)
		}
	}
	out := map[bson.ObjectID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	var adjs []models.StockAdjustment
	if err := findAll(ctx, mdb.StockAdjustments(), bson.M{"delta": bson.M{"$gt": 0}, "lots.lot_id": bson.M{"$in": ids}}, &adjs); err != nil {
		return nil, err
	}
	for _, a := range adjs {
		for _, l := range a.Lots {
			at, ok := created[l.LotID]
			if !ok {
				continue
			}
			if d := a.CreatedAt.Sub(at); d > -legacyLotWindow && d < legacyLotWindow {
				out[l.LotID] = true
			}
		}
	}
	return out, nil
}

func saleMoves(ctx context.Context, mdb *db.MongoDB, q MovementQuery) ([]Movement, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$lookup", Value: bson.M{"from": "sales", "localField": "sale_id", "foreignField": "_id", "as": "sale"}}},
		{{Key: "$unwind", Value: "$sale"}},
		{{Key: "$match", Value: bson.M{"sale.sold_at": inPeriod(q), "sale.voided": bson.M{"$ne": true}}}},
	}
	if q.DrugName != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"drug_name": nameLike(q.DrugName)}}})
	}
	cur, err := mdb.SaleItems().Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID       bson.ObjectID `bson:"_id"`
		DrugID   bson.ObjectID `bson:"drug_id"`
		DrugName string        `bson:"drug_name"`
		Qty      int           `bson:"qty"`
		Sale     struct {
			BillNo string    `bson:"bill_no"`
			SoldAt time.Time `bson:"sold_at"`
		} `bson:"sale"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]Movement, 0, len(rows))
	for _, r := range rows {
		out = append(out, Movement{
			ID: r.ID.Hex(), Type: MoveSale, DrugID: r.DrugID.Hex(), DrugName: r.DrugName,
			Delta: -r.Qty, Reference: r.Sale.BillNo, At: r.Sale.SoldAt,
		})
	}
	return out, nil
}

func returnMoves(ctx context.Context, mdb *db.MongoDB, q MovementQuery) ([]Movement, error) {
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"returned_at": inPeriod(q)}}},
		{{Key: "$unwind", Value: "$items"}},
	}
	if q.DrugName != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"items.drug_name": nameLike(q.DrugName)}}})
	}
	cur, err := mdb.DrugReturns().Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID         bson.ObjectID `bson:"_id"`
		ReturnNo   string        `bson:"return_no"`
		ReturnedAt time.Time     `bson:"returned_at"`
		Items      struct {
			DrugID   bson.ObjectID `bson:"drug_id"`
			DrugName string        `bson:"drug_name"`
			Qty      int           `bson:"qty"`
		} `bson:"items"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]Movement, 0, len(rows))
	for _, r := range rows {
		out = append(out, Movement{
			ID: r.ID.Hex(), Type: MoveReturn, DrugID: r.Items.DrugID.Hex(), DrugName: r.Items.DrugName,
			Delta: r.Items.Qty, Reference: r.ReturnNo, At: r.ReturnedAt,
		})
	}
	return out, nil
}

func adjustmentMoves(ctx context.Context, mdb *db.MongoDB, q MovementQuery) ([]Movement, error) {
	filter := bson.M{"created_at": inPeriod(q)}
	if q.DrugName != "" {
		filter["drug_name"] = nameLike(q.DrugName)
	}
	var adjs []models.StockAdjustment
	if err := findAll(ctx, mdb.StockAdjustments(), filter, &adjs); err != nil {
		return nil, err
	}
	out := make([]Movement, 0, len(adjs))
	for _, a := range adjs {
		out = append(out, Movement{
			ID: a.ID.Hex(), Type: MoveAdjustment, DrugID: a.DrugID.Hex(), DrugName: a.DrugName,
			Delta: a.Delta, Reference: a.Reason, Note: a.Note, At: a.CreatedAt,
		})
	}
	return out, nil
}

func writeoffMoves(ctx context.Context, mdb *db.MongoDB, q MovementQuery) ([]Movement, error) {
	filter := bson.M{"created_at": inPeriod(q)}
	if q.DrugName != "" {
		filter["drug_name"] = nameLike(q.DrugName)
	}
	var wos []models.LotWriteoff
	if err := findAll(ctx, mdb.LotWriteoffs(), filter, &wos); err != nil {
		return nil, err
	}
	out := make([]Movement, 0, len(wos))
	for _, w := range wos {
		qty := w.Qty
		if w.Reason == deletedLot {
			if w.Received == nil {
				continue
			}
			qty -= *w.Received
		}
		if qty == 0 {
			continue
		}
		out = append(out, Movement{
			ID: w.ID.Hex(), Type: MoveWriteoff, DrugID: w.DrugID.Hex(), DrugName: w.DrugName,
			Delta: -qty, Reference: w.LotNumber, At: w.CreatedAt,
		})
	}
	return out, nil
}

func findAll(ctx context.Context, c *mongo.Collection, filter bson.M, out any) error {
	cur, err := c.Find(ctx, filter)
	if err != nil {
		return err
	}
	return cur.All(ctx, out)
}
