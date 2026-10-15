package catalog

import (
	"context"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/refusal"
)

// MaxImportRows is how many drugs one bulk import may carry.
const MaxImportRows = 1000

// ErrDuplicateBarcode refuses a drug whose barcode another drug already has
// (the unique barcode index).
var ErrDuplicateBarcode = &refusal.Error{Kind: refusal.Conflict, Msg: "บาร์โค้ดนี้มีอยู่ในระบบแล้ว"}

// Register records a new drug and the lot its opening stock is in, in one
// transaction (ADR-0007: stock and lots agree from the first moment).
func Register(ctx context.Context, mdb *db.MongoDB, in models.DrugInput) (models.Drug, error) {
	tz := mdb.Timezone(ctx)
	drug, lot, err := NewDrug(in, time.Now().In(tz), tz)
	if err != nil {
		return models.Drug{}, err
	}
	err = insert(ctx, mdb, &drug, func(d models.Drug) *models.DrugLot {
		if lot == nil {
			return nil
		}
		opening := *lot
		opening.DrugID = d.ID
		opening.DrugName = d.Name
		if opening.CostPrice == nil {
			cost := d.CostPrice
			opening.CostPrice = &cost
		}
		return &opening
	})
	return drug, err
}

// ImportRowError is one bulk-import row that was not recorded. Row counts
// from 2: row 1 is the spreadsheet's header.
type ImportRowError struct {
	Row     int    `json:"row"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

// ImportResult is how many rows a bulk import recorded and why the others
// were not.
type ImportResult struct {
	Imported int              `json:"imported"`
	Errors   []ImportRowError `json:"errors"`
}

// Import records each drug on its own, so one bad row does not fail the
// batch. Stock imported without lot data goes into an OPENING lot.
func Import(ctx context.Context, mdb *db.MongoDB, inputs []models.DrugInput) (ImportResult, error) {
	if len(inputs) == 0 {
		return ImportResult{}, refusal.Invalidf("drugs is required")
	}
	if len(inputs) > MaxImportRows {
		return ImportResult{}, refusal.Invalidf("ไม่เกิน 1,000 รายการต่อครั้ง")
	}
	result := ImportResult{Errors: []ImportRowError{}}
	for i, in := range inputs {
		row := i + 2
		if strings.TrimSpace(in.Name) == "" {
			result.Errors = append(result.Errors, ImportRowError{Row: row, Name: "-", Message: "ชื่อยาห้ามว่าง"})
			continue
		}
		if err := importOne(ctx, mdb, in); err != nil {
			result.Errors = append(result.Errors, ImportRowError{Row: row, Name: in.Name, Message: rowMessage(err)})
			continue
		}
		result.Imported++
	}
	return result, nil
}

const importRowTimeout = 5 * time.Second

func importOne(ctx context.Context, mdb *db.MongoDB, in models.DrugInput) error {
	drug, err := ImportedDrug(in, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, importRowTimeout)
	defer cancel()
	return insert(ctx, mdb, &drug, func(d models.Drug) *models.DrugLot {
		if d.Stock <= 0 {
			return nil
		}
		lot := inventory.OpeningLot(d)
		return &lot
	})
}

// rowMessage is what a row's report says: a refusal's own reason, or a
// generic failure that does not leak database detail.
func rowMessage(err error) string {
	if e, ok := err.(*refusal.Error); ok {
		return e.Msg
	}
	return "บันทึกไม่สำเร็จ"
}

// insert writes the drug and, when openingLot gives one, its opening lot.
func insert(ctx context.Context, mdb *db.MongoDB, drug *models.Drug, openingLot func(models.Drug) *models.DrugLot) error {
	err := mdb.WithTransaction(ctx, func(txCtx context.Context) error {
		res, err := mdb.Drugs().InsertOne(txCtx, *drug)
		if err != nil {
			return err
		}
		drug.ID = res.InsertedID.(bson.ObjectID)
		if lot := openingLot(*drug); lot != nil {
			return inventory.OpenStock(txCtx, mdb, *lot)
		}
		return nil
	})
	if db.IsDuplicateKey(err) {
		return ErrDuplicateBarcode
	}
	return err
}

// Update stores an edit of the drug with the given id.
func Update(ctx context.Context, mdb *db.MongoDB, id string, in models.DrugUpdate) error {
	oid, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return refusal.Invalidf("invalid id")
	}
	in, err = Edit(in)
	if err != nil {
		return err
	}
	res, err := mdb.Drugs().UpdateOne(ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{
			"name":         in.Name,
			"generic_name": in.GenericName,
			"type":         in.Type,
			"strength":     in.Strength,
			"barcode":      in.Barcode,
			"price":        in.SellPrice, // bson key = "price", NOT "sell_price"
			"cost_price":   in.CostPrice,
			"min_stock":    in.MinStock,
			"reg_no":       in.RegNo,
			"unit":         in.Unit,
			"report_types": in.ReportTypes,
			"alt_units":    in.AltUnits,
			"prices":       in.Prices,
		}},
	)
	if db.IsDuplicateKey(err) {
		return ErrDuplicateBarcode
	}
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return refusal.NotFoundf("drug not found")
	}
	return nil
}
