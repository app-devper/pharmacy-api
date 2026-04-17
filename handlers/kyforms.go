package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/db"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type KyHandler struct{ dbm *db.Manager }

func NewKyHandler(d *db.Manager) *KyHandler { return &KyHandler{dbm: d} }

func kyFilter(month string) bson.M {
	if month == "" {
		return bson.M{}
	}
	return bson.M{"date": bson.M{"$regex": "^" + month}}
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
	cur.All(ctx, &rows)
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
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := models.Ky9{
		Date: input.Date, DrugName: input.DrugName, RegNo: input.RegNo,
		Unit: input.Unit, Qty: input.Qty, PricePerUnit: input.PricePerUnit,
		TotalValue: input.PricePerUnit * float64(input.Qty),
		Seller: input.Seller, InvoiceNo: input.InvoiceNo, CreatedAt: time.Now(),
	}
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
	cur.All(ctx, &rows)
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
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := models.Ky10{
		Date: input.Date, DrugName: input.DrugName, RegNo: input.RegNo,
		Qty: input.Qty, Unit: input.Unit, BuyerName: input.BuyerName,
		BuyerAddress: input.BuyerAddress, RxNo: input.RxNo, Doctor: input.Doctor,
		Balance: input.Balance, CreatedAt: time.Now(),
	}
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
	cur.All(ctx, &rows)
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
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := models.Ky11{
		Date: input.Date, DrugName: input.DrugName, RegNo: input.RegNo,
		Qty: input.Qty, Unit: input.Unit, BuyerName: input.BuyerName,
		Purpose: input.Purpose, Pharmacist: input.Pharmacist, CreatedAt: time.Now(),
	}
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
	cur.All(ctx, &rows)
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
	if input.Status == "" {
		input.Status = "จ่ายแล้ว"
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	doc := models.Ky12{
		Date: input.Date, RxNo: input.RxNo, PatientName: input.PatientName,
		Doctor: input.Doctor, Hospital: input.Hospital, DrugName: input.DrugName,
		Qty: input.Qty, Unit: input.Unit, TotalValue: input.TotalValue,
		Status: input.Status, CreatedAt: time.Now(),
	}
	res, err := mdb.Ky12().InsertOne(ctx, doc)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"id": res.InsertedID.(bson.ObjectID).Hex()})
}
