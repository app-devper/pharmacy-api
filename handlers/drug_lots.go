package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/bson"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/inventory"
	mw "pharmacy-pos/backend/middleware"
	"pharmacy-pos/backend/models"
)

type DrugLotHandler struct{ dbm *db.Manager }

func NewDrugLotHandler(d *db.Manager) *DrugLotHandler { return &DrugLotHandler{dbm: d} }

// ListLots returns all lots for a drug in the order sales take from them.
func (h *DrugLotHandler) ListLots(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	drugOID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		jsonError(w, "invalid drug id", http.StatusBadRequest)
		return
	}

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	lots, err := inventory.Lots(ctx, mdb, drugOID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, lots)
}

// AddLot receives a lot entered by hand (inventory: also settles oversold stock).
func (h *DrugLotHandler) AddLot(w http.ResponseWriter, r *http.Request) {
	var input models.DrugLotInput
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

	lot, err := inventory.AddLot(ctx, mdb, chi.URLParam(r, "id"), input)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, lot)
}

// Expiring returns sellable lots in an expiry window (written-off lots are already dealt with).
// GET /api/pharmacy/v1/lots/expiring?days=60         — lots expiring within N days (default from Settings, includes already-expired)
// GET /api/pharmacy/v1/lots/expiring?expired_only=true — only lots whose expiry_date is already in the past
func (h *DrugLotHandler) Expiring(w http.ResponseWriter, r *http.Request) {
	expiredOnly := r.URL.Query().Get("expired_only") == "true"

	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Default window comes from tenant settings; `?days=N` overrides per-request.
	days := loadStockSettings(ctx, mdb).ExpiringDays
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		days = d
	}

	lots, err := inventory.Expiring(ctx, mdb, time.Now(), days, expiredOnly)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, lots)
}

// WriteoffLots writes off every listed lot or none (inventory, ADR-0007).
// POST /api/pharmacy/v1/lots/writeoff   body: {"lot_ids": ["<hex>", ...]}
func (h *DrugLotHandler) WriteoffLots(w http.ResponseWriter, r *http.Request) {
	var input struct {
		LotIDs []string `json:"lot_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.LotIDs) == 0 {
		jsonError(w, "lot_ids required", http.StatusBadRequest)
		return
	}
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	n, err := inventory.WriteOff(ctx, mdb, input.LotIDs)
	var lotErr *inventory.WriteOffError
	if errors.As(err, &lotErr) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"written_off": 0,
			"failed":      []map[string]string{{"lot_id": lotErr.LotID, "error": lotErr.Error()}},
		})
		return
	}
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, map[string]interface{}{"written_off": n, "failed": []map[string]string{}})
}

// DeleteLot removes a lot entered by mistake; a lot already sold from must be
// written off instead (409).
func (h *DrugLotHandler) DeleteLot(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := inventory.DeleteLot(ctx, mdb, chi.URLParam(r, "id"), chi.URLParam(r, "lot_id")); err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, map[string]bool{"ok": true})
}

// Drift lists lot-tracked drugs whose stock and lots disagree (read-only).
// GET /api/pharmacy/v1/inventory/drift
func (h *DrugLotHandler) Drift(w http.ResponseWriter, r *http.Request) {
	mdb, err := h.dbm.ForClient(mw.GetClientID(r.Context()))
	if err != nil {
		jsonError(w, "unauthorized client", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	rows, err := inventory.Drift(ctx, mdb)
	if err != nil {
		writeCommandError(w, err)
		return
	}
	jsonOK(w, rows)
}
