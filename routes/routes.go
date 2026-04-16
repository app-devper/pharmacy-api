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
	origin string,
	secretKey string,
) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(mw.CORS(origin))

	r.Route("/api", func(r chi.Router) {
		r.Use(mw.RequireAuth(secretKey))

		// Drugs
		r.Get("/drugs", dh.List)
		r.Get("/drugs/low-stock", dh.LowStock)
		r.Post("/drugs", dh.Add)
		r.Put("/drugs/{id}", dh.Update)

		// Stock Adjustments (audit log)
		r.Post("/drugs/{id}/adjustments", ah.Create)
		r.Get("/drugs/{id}/adjustments", ah.List)

		// Drug Lots (FEFO)
		r.Get("/drugs/{id}/lots", lh.ListLots)
		r.Post("/drugs/{id}/lots", lh.AddLot)
		r.Delete("/drugs/{id}/lots/{lot_id}", lh.DeleteLot)
		r.Get("/lots/expiring", lh.Expiring)
		r.Post("/lots/writeoff", lh.WriteoffLots)

		// Customers
		r.Get("/customers", ch.List)
		r.Post("/customers", ch.Add)
		r.Put("/customers/{id}", ch.Update)
		r.Get("/customers/{id}/sales", ch.GetSales)

		// Sales
		r.Get("/sales", sh.List)
		r.Post("/sales", sh.Create)
		r.Get("/sales/{id}/items", sh.Items)
		r.Post("/sales/{id}/void", sh.Void)
		r.Post("/sales/{id}/return", reth.Create)
		r.Get("/sales/{id}/returns", reth.List)

		// Reports
		r.Get("/report/summary", rh.Summary)
		r.Get("/report/daily", rh.Daily)
		r.Get("/report/eod", rh.Eod)
		r.Get("/report/profit", rh.Profit)
		r.Get("/report/top-drugs", rh.TopDrugs)
		r.Get("/report/slow-drugs", rh.SlowDrugs)
		r.Get("/report/monthly", rh.Monthly)

		// KY Forms
		r.Get("/ky9", kh.ListKy9)
		r.Post("/ky9", kh.AddKy9)
		r.Get("/ky10", kh.ListKy10)
		r.Post("/ky10", kh.AddKy10)
		r.Get("/ky11", kh.ListKy11)
		r.Post("/ky11", kh.AddKy11)
		r.Get("/ky12", kh.ListKy12)
		r.Post("/ky12", kh.AddKy12)

		// Import / Purchase Orders
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
	})

	return r
}
