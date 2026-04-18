package routes

import (
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"pharmacy-pos/backend/handlers"
	mw "pharmacy-pos/backend/middleware"
)

const (
	RoleSUPER = "SUPER"
	RoleADMIN = "ADMIN"
	RoleUSER  = "USER"
)

func Setup(
	dh *handlers.DrugHandler,
	lh *handlers.DrugLotHandler,
	ch *handlers.CustomerHandler,
	sh *handlers.SaleHandler,
	rh *handlers.ReportHandler,
	kh *handlers.KyHandler,
	eh *handlers.ExportHandler,
	ih *handlers.ImportHandler,
	suph *handlers.SupplierHandler,
	ah *handlers.StockAdjustmentHandler,
	reth *handlers.ReturnHandler,
	mvh *handlers.MovementsHandler,
	seth *handlers.SettingsHandler,
	secretKey string,
	authSystem string,
) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(mw.CORS())

	r.Route("/api", func(r chi.Router) {
		r.Use(mw.RequireAuth(secretKey, authSystem))

		// ── USER + ADMIN + SUPER ──────────────────────────────────
		r.Group(func(r chi.Router) {
			// Drugs (read)
			r.Get("/drugs", dh.List)
			r.Get("/drugs/low-stock", dh.LowStock)
			r.Get("/drugs/{id}/lots", lh.ListLots)
			r.Get("/lots/expiring", lh.Expiring)

			// Customers (read + add)
			r.Get("/customers", ch.List)
			r.Post("/customers", ch.Add)
			r.Get("/customers/{id}/sales", ch.GetSales)

			// Sales (create + view + return)
			r.Get("/sales", sh.List)
			r.Post("/sales", sh.Create)
			r.Get("/sales/{id}/items", sh.Items)
			r.Post("/sales/{id}/return", reth.Create)
			r.Get("/sales/{id}/returns", reth.List)

			// Reports (dashboard only)
			r.Get("/report/summary", rh.Summary)
			r.Get("/report/dashboard", rh.Dashboard)
			r.Get("/report/daily", rh.Daily)
			r.Get("/report/monthly", rh.Monthly)
			r.Get("/report/top-drugs", rh.TopDrugs)
			r.Get("/report/slow-drugs", rh.SlowDrugs)

			// Movements (read-only audit)
			r.Get("/movements", mvh.List)

			// Settings (read-only for all authenticated users — receipts need store info)
			r.Get("/settings", seth.Get)
		})

		// ── ADMIN + SUPER only ────────────────────────────────────
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole(RoleADMIN))

			// Drug management
			r.Post("/drugs", dh.Add)
			r.Post("/drugs/bulk", dh.BulkImport)
			r.Get("/drugs/reorder-suggestions", dh.ReorderSuggestions)
			r.Put("/drugs/{id}", dh.Update)
			r.Post("/drugs/{id}/adjustments", ah.Create)
			r.Get("/drugs/{id}/adjustments", ah.List)
			r.Post("/drugs/{id}/lots", lh.AddLot)
			r.Delete("/drugs/{id}/lots/{lot_id}", lh.DeleteLot)
			r.Post("/lots/writeoff", lh.WriteoffLots)

			// Customer edit
			r.Put("/customers/{id}", ch.Update)

			// Sales admin
			r.Post("/sales/{id}/void", sh.Void)

			// Financial reports
			r.Get("/report/eod", rh.Eod)
			r.Get("/report/profit", rh.Profit)

			// KY Forms
			r.Get("/ky9", kh.ListKy9)
			r.Post("/ky9", kh.AddKy9)
			r.Get("/ky10", kh.ListKy10)
			r.Post("/ky10", kh.AddKy10)
			r.Get("/ky11", kh.ListKy11)
			r.Post("/ky11", kh.AddKy11)
			r.Get("/ky12", kh.ListKy12)
			r.Post("/ky12", kh.AddKy12)

			// Imports
			r.Get("/imports", ih.List)
			r.Post("/imports", ih.Create)
			r.Get("/imports/{id}", ih.GetOne)
			r.Put("/imports/{id}", ih.Update)
			r.Post("/imports/{id}/confirm", ih.Confirm)
			r.Delete("/imports/{id}", ih.Delete)

			// Suppliers
			r.Get("/suppliers", suph.List)
			r.Post("/suppliers", suph.Create)
			r.Put("/suppliers/{id}", suph.Update)
			r.Delete("/suppliers/{id}", suph.Delete)

			// PDF Export
			r.Get("/export/{form}", eh.Export)

			// Settings (write)
			r.Put("/settings", seth.Update)
		})
	})

	return r
}
