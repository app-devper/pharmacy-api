package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type KyHandler struct{ dbm *db.Manager }

func NewKyHandler(d *db.Manager) *KyHandler { return &KyHandler{dbm: d} }

// errBadRequest is a sentinel-style typed error so handlers can map a
// validateKyXInput failure to a 400 response without string-parsing.
type errBadRequest string

func (e errBadRequest) Error() string { return string(e) }

// validateKy9Input rejects KY9 (controlled-drug purchase) submissions that
// would corrupt the regulator's audit trail: missing date / drug, non-positive
// qty, or negative price. KY records cannot be silently re-edited later, so
// the input must be sane on first write.
func validateKy9Input(in models.Ky9Input) error {
	if strings.TrimSpace(in.Date) == "" {
		return errBadRequest("date is required")
	}
	if strings.TrimSpace(in.DrugName) == "" {
		return errBadRequest("drug_name is required")
	}
	if in.Qty <= 0 {
		return errBadRequest("qty must be > 0")
	}
	if in.PricePerUnit < 0 {
		return errBadRequest("price_per_unit must be >= 0")
	}
	return nil
}

// validateKy10Input rejects KY10 (controlled-drug sale) submissions missing
// the regulator-mandated buyer fields or with non-positive qty / negative
// balance. The KMP frontend already validates these, but the backend mirrors
// the check so any client (curl, future flutter app, retry from a malformed
// queue) can't bypass it.
func validateKy10Input(in models.Ky10Input) error {
	if strings.TrimSpace(in.Date) == "" {
		return errBadRequest("date is required")
	}
	if strings.TrimSpace(in.DrugName) == "" {
		return errBadRequest("drug_name is required")
	}
	if in.Qty <= 0 {
		return errBadRequest("qty must be > 0")
	}
	if strings.TrimSpace(in.BuyerName) == "" {
		return errBadRequest("buyer_name is required")
	}
	if strings.TrimSpace(in.BuyerAddress) == "" {
		return errBadRequest("buyer_address is required")
	}
	if in.Balance < 0 {
		return errBadRequest("balance must be >= 0")
	}
	return nil
}

// validateKy11Input rejects KY11 (dangerous-drug sale) submissions missing
// the regulator-mandated buyer / purpose / pharmacist fields.
func validateKy11Input(in models.Ky11Input) error {
	if strings.TrimSpace(in.Date) == "" {
		return errBadRequest("date is required")
	}
	if strings.TrimSpace(in.DrugName) == "" {
		return errBadRequest("drug_name is required")
	}
	if in.Qty <= 0 {
		return errBadRequest("qty must be > 0")
	}
	if strings.TrimSpace(in.BuyerName) == "" {
		return errBadRequest("buyer_name is required")
	}
	if strings.TrimSpace(in.Purpose) == "" {
		return errBadRequest("purpose is required")
	}
	if strings.TrimSpace(in.Pharmacist) == "" {
		return errBadRequest("pharmacist is required")
	}
	return nil
}

// validateKy12Input rejects KY12 (prescription-drug sale) submissions missing
// the rx_no / patient / doctor fields the regulator requires, or with a
// negative total_value. total_value is client-supplied (the line-level value
// already accounts for unit conversion + tier pricing on the cashier side);
// the backend can only sanity-check the sign.
func validateKy12Input(in models.Ky12Input) error {
	if strings.TrimSpace(in.Date) == "" {
		return errBadRequest("date is required")
	}
	if strings.TrimSpace(in.DrugName) == "" {
		return errBadRequest("drug_name is required")
	}
	if in.Qty <= 0 {
		return errBadRequest("qty must be > 0")
	}
	if strings.TrimSpace(in.RxNo) == "" {
		return errBadRequest("rx_no is required")
	}
	if strings.TrimSpace(in.PatientName) == "" {
		return errBadRequest("patient_name is required")
	}
	if strings.TrimSpace(in.Doctor) == "" {
		return errBadRequest("doctor is required")
	}
	if in.TotalValue < 0 {
		return errBadRequest("total_value must be >= 0")
	}
	return nil
}

func kyFilter(month string) bson.M {
	if month == "" {
		return bson.M{}
	}
	return bson.M{"date": bson.M{"$regex": "^" + regexp.QuoteMeta(month)}}
}

func buildKy9Payload(in models.Ky9Input, now time.Time) models.Ky9 {
	return models.Ky9{
		SaleID:       strings.TrimSpace(in.SaleID),
		Date:         in.Date,
		DrugName:     in.DrugName,
		RegNo:        in.RegNo,
		Unit:         in.Unit,
		Qty:          in.Qty,
		PricePerUnit: in.PricePerUnit,
		TotalValue:   in.PricePerUnit * float64(in.Qty),
		Seller:       in.Seller,
		InvoiceNo:    in.InvoiceNo,
		CreatedAt:    now,
	}
}

func buildKy10Payload(in models.Ky10Input, now time.Time) models.Ky10 {
	return models.Ky10{
		SaleID:       strings.TrimSpace(in.SaleID),
		Date:         in.Date,
		DrugName:     in.DrugName,
		RegNo:        in.RegNo,
		Qty:          in.Qty,
		Unit:         in.Unit,
		BuyerName:    in.BuyerName,
		BuyerAddress: in.BuyerAddress,
		RxNo:         in.RxNo,
		Doctor:       in.Doctor,
		Balance:      in.Balance,
		CreatedAt:    now,
	}
}

func buildKy11Payload(in models.Ky11Input, now time.Time) models.Ky11 {
	return models.Ky11{
		SaleID:     strings.TrimSpace(in.SaleID),
		Date:       in.Date,
		DrugName:   in.DrugName,
		RegNo:      in.RegNo,
		Qty:        in.Qty,
		Unit:       in.Unit,
		BuyerName:  in.BuyerName,
		Purpose:    in.Purpose,
		Pharmacist: in.Pharmacist,
		CreatedAt:  now,
	}
}

func buildKy12Payload(in models.Ky12Input, now time.Time) models.Ky12 {
	status := in.Status
	if status == "" {
		status = "จ่ายแล้ว"
	}
	return models.Ky12{
		SaleID:      strings.TrimSpace(in.SaleID),
		Date:        in.Date,
		RxNo:        in.RxNo,
		PatientName: in.PatientName,
		Doctor:      in.Doctor,
		Hospital:    in.Hospital,
		DrugName:    in.DrugName,
		Qty:         in.Qty,
		Unit:        in.Unit,
		TotalValue:  in.TotalValue,
		Status:      status,
		CreatedAt:   now,
	}
}

func (h *KyHandler) ListKy9(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cur, err := mdb.Ky9().Find(ctx, kyFilter(month), options.Find().SetSort(bson.D{{Key: "date", Value: -1}}))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)
	var rows []models.Ky9
	if err := cur.All(ctx, &rows); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []models.Ky9{}
	}
	jsonOK(w, rows)
}

func (h *KyHandler) AddKy9(w http.ResponseWriter, r *http.Request) {
	var input models.Ky9Input
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := validateKy9Input(input); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := buildKy9Payload(input, time.Now())
	res, err := mdb.Ky9().InsertOne(ctx, doc)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{"id": res.InsertedID.(bson.ObjectID).Hex(), "total_value": doc.TotalValue})
}

func (h *KyHandler) ListKy10(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cur, err := mdb.Ky10().Find(ctx, kyFilter(month), options.Find().SetSort(bson.D{{Key: "date", Value: -1}}))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)
	var rows []models.Ky10
	if err := cur.All(ctx, &rows); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []models.Ky10{}
	}
	jsonOK(w, rows)
}

func (h *KyHandler) AddKy10(w http.ResponseWriter, r *http.Request) {
	var input models.Ky10Input
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := validateKy10Input(input); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := buildKy10Payload(input, time.Now())
	res, err := mdb.Ky10().InsertOne(ctx, doc)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"id": res.InsertedID.(bson.ObjectID).Hex()})
}

func (h *KyHandler) ListKy11(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cur, err := mdb.Ky11().Find(ctx, kyFilter(month), options.Find().SetSort(bson.D{{Key: "date", Value: -1}}))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)
	var rows []models.Ky11
	if err := cur.All(ctx, &rows); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []models.Ky11{}
	}
	jsonOK(w, rows)
}

func (h *KyHandler) AddKy11(w http.ResponseWriter, r *http.Request) {
	var input models.Ky11Input
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := validateKy11Input(input); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := buildKy11Payload(input, time.Now())
	res, err := mdb.Ky11().InsertOne(ctx, doc)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"id": res.InsertedID.(bson.ObjectID).Hex()})
}

func (h *KyHandler) ListKy12(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cur, err := mdb.Ky12().Find(ctx, kyFilter(month), options.Find().SetSort(bson.D{{Key: "date", Value: -1}}))
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)
	var rows []models.Ky12
	if err := cur.All(ctx, &rows); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []models.Ky12{}
	}
	jsonOK(w, rows)
}

func (h *KyHandler) AddKy12(w http.ResponseWriter, r *http.Request) {
	var input models.Ky12Input
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := validateKy12Input(input); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := buildKy12Payload(input, time.Now())
	res, err := mdb.Ky12().InsertOne(ctx, doc)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"id": res.InsertedID.(bson.ObjectID).Hex()})
}

func (h *KyHandler) BySale(w http.ResponseWriter, r *http.Request) {
	saleID := strings.TrimSpace(chi.URLParam(r, "id"))
	if saleID == "" {
		jsonError(w, "sale id is required", http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	filter := bson.M{"sale_id": saleID}
	sort := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})

	out := models.SaleKyLinkage{
		Ky10: []models.Ky10{},
		Ky11: []models.Ky11{},
		Ky12: []models.Ky12{},
	}

	if cur, err := mdb.Ky10().Find(ctx, filter, sort); err == nil {
		defer cur.Close(ctx)
		if err := cur.All(ctx, &out.Ky10); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if cur, err := mdb.Ky11().Find(ctx, filter, sort); err == nil {
		defer cur.Close(ctx)
		if err := cur.All(ctx, &out.Ky11); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if cur, err := mdb.Ky12().Find(ctx, filter, sort); err == nil {
		defer cur.Close(ctx)
		if err := cur.All(ctx, &out.Ky12); err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if out.Ky10 == nil {
		out.Ky10 = []models.Ky10{}
	}
	if out.Ky11 == nil {
		out.Ky11 = []models.Ky11{}
	}
	if out.Ky12 == nil {
		out.Ky12 = []models.Ky12{}
	}
	jsonOK(w, out)
}
