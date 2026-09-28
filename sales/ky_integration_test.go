package sales

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
)

// kyDrug makes the shop's drug need ขย.10 and ขย.12, sold by the tablet or
// by the strip of 5.
func (s *shop) kyDrug() {
	s.t.Helper()
	if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": s.drugID}, bson.M{"$set": bson.M{
		"report_types": []string{"KY10", "ky12"}, "unit": "เม็ด", "reg_no": "1A 23/45",
		"alt_units": []models.AltUnit{{Name: "แผง", Factor: 5, SellPrice: 45}},
	}}); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shop) settings(set bson.M) {
	s.t.Helper()
	set["key"] = db.SettingsKey
	if _, err := s.mdb.Settings().InsertOne(s.ctx, set); err != nil {
		s.t.Fatal(err)
	}
}

func capture() *models.SaleKyCapture {
	return &models.SaleKyCapture{
		Ky10: &models.Ky10Capture{BuyerName: " สมชาย ", BuyerAddress: "กทม.", RxNo: "RX1", Doctor: "นพ.ก"},
		Ky12: &models.Ky12Capture{RxNo: "RX1", PatientName: "สมชาย", Doctor: "นพ.ก", Hospital: "รพ.ข"},
	}
}

func (s *shop) kyCount() (ky10, ky11, ky12 int64) {
	return s.count(s.mdb.Ky10()), s.count(s.mdb.Ky11()), s.count(s.mdb.Ky12())
}

// The forms are recorded with the sale, from the sale: the unit sold, what
// was paid, the business day, and the stock left.
func TestSaleRecordsItsKyFormsFromTheSale(t *testing.T) {
	s := newShop(t)
	s.kyDrug()
	in := models.SaleInput{
		ClientRequestID: "ky-1",
		Items:           []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 5, Unit: "แผง", UnitFactor: 5}},
		Discount:        9, // 45 − 9: paid 36
		Received:        100,
		Ky:              capture(),
	}
	out, _, err := Sell(s.ctx, s.mdb, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.KyStatus != models.KyRecorded {
		t.Fatalf("ky_status %q", out.KyStatus)
	}
	var k10 models.Ky10
	if err := s.mdb.Ky10().FindOne(s.ctx, bson.M{"sale_id": out.ID}).Decode(&k10); err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(s.mdb.Timezone(s.ctx)).Format("2006-01-02")
	if k10.Qty != 1 || k10.Unit != "แผง" || k10.Balance != 5 || k10.Date != today || k10.BuyerName != "สมชาย" || k10.RegNo != "1A 23/45" {
		t.Fatalf("ky10 %+v", k10)
	}
	var k12 models.Ky12
	if err := s.mdb.Ky12().FindOne(s.ctx, bson.M{"sale_id": out.ID}).Decode(&k12); err != nil {
		t.Fatal(err)
	}
	if k12.TotalValue != 36 || k12.Qty != 1 || k12.Status != "จ่ายแล้ว" {
		t.Fatalf("ky12 %+v", k12)
	}

	// Repeating the request records nothing more.
	if _, replayed, err := Sell(s.ctx, s.mdb, in); err != nil || !replayed {
		t.Fatalf("repeat: %v replayed=%v", err, replayed)
	}
	if k10n, _, k12n := s.kyCount(); k10n != 1 || k12n != 1 {
		t.Fatalf("after repeat: %d ky10, %d ky12", k10n, k12n)
	}
}

func TestIncompleteKyRefusesTheWholeSale(t *testing.T) {
	s := newShop(t)
	s.kyDrug()
	c := capture()
	c.Ky12.Doctor = " "
	_, _, err := Sell(s.ctx, s.mdb, models.SaleInput{
		Items: []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 1}}, Received: 100, Ky: c,
	})
	wantKind(t, err, Invalid)
	if drug, _ := s.stock(); drug != 10 || s.count(s.mdb.Sales()) != 0 || s.count(s.mdb.Ky10()) != 0 {
		t.Fatal("a refused sale left something behind")
	}
}

func TestKyStatusSaysHowTheSaleMetItsObligations(t *testing.T) {
	s := newShop(t)
	sell := func(in models.SaleInput) string {
		t.Helper()
		in.Items = []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 1}}
		in.Received = 100
		out, _, err := Sell(s.ctx, s.mdb, in)
		if err != nil {
			t.Fatal(err)
		}
		return out.KyStatus
	}
	if got := sell(models.SaleInput{}); got != models.KyNone {
		t.Fatalf("plain drug: %q", got)
	}
	s.kyDrug()
	if got := sell(models.SaleInput{}); got != models.KySeparate {
		t.Fatalf("no capture: %q", got)
	}
	if got := sell(models.SaleInput{KySkippedByCashier: true}); got != models.KySkippedCashier {
		t.Fatalf("cashier skip: %q", got)
	}
	s.settings(bson.M{"ky": bson.M{"skip_auto": true}})
	if got := sell(models.SaleInput{Ky: capture()}); got != models.KySkippedSetting {
		t.Fatalf("shop skip: %q", got)
	}
	if k10, _, k12 := s.kyCount(); k10+k12 != 0 {
		t.Fatalf("skipped or separate sales recorded forms: %d %d", k10, k12)
	}
}

func TestEmptyKyFieldsTakeTheShopDefaults(t *testing.T) {
	s := newShop(t)
	if _, err := s.mdb.Drugs().UpdateOne(s.ctx, bson.M{"_id": s.drugID}, bson.M{"$set": bson.M{"report_types": []string{"ky11"}}}); err != nil {
		t.Fatal(err)
	}
	s.settings(bson.M{"pharmacist": bson.M{"name": "ภก.สมหญิง"}})
	out, _, err := Sell(s.ctx, s.mdb, models.SaleInput{
		Items: []models.SaleItemInput{{DrugID: s.drugID.Hex(), Qty: 2}}, Received: 100,
		Ky: &models.SaleKyCapture{Ky11: &models.Ky11Capture{BuyerName: "ก", Purpose: "ไอ"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var k11 models.Ky11
	if err := s.mdb.Ky11().FindOne(s.ctx, bson.M{"sale_id": out.ID}).Decode(&k11); err != nil {
		t.Fatal(err)
	}
	if k11.Pharmacist != "ภก.สมหญิง" || k11.Qty != 2 {
		t.Fatalf("ky11 %+v", k11)
	}
}
