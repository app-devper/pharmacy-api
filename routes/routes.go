package routes

import (
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"pharmacy-pos/backend/handlers"
	mw "pharmacy-pos/backend/middleware"
)

const (
	RoleSUPER   = "SUPER"
	RoleADMIN   = "ADMIN"
	RoleMANAGER = "MANAGER"
	RoleUSER    = "USER"
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
	sch *handlers.StockCountHandler,
	reth *handlers.ReturnHandler,
	mvh *handlers.MovementsHandler,
	seth *handlers.SettingsHandler,
	labh *handlers.LabelHandler,
	secretKey string,
	authSystem string,
	allowedOrigins []string,
	gatewayHosts string,
	live *mw.LiveIdentity,
) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.Logger)
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.RequestSize(5 * 1024 * 1024))
	r.Use(mw.RequireGatewayHost(gatewayHosts))
	r.Use(mw.CORS(allowedOrigins))

	r.Route("/api/pharmacy/v1", func(r chi.Router) {
		r.Use(mw.RequireAuth(secretKey, authSystem))

		// Route permissions follow the shared role policy (KMP ADR-0004):
		// USER < MANAGER < ADMIN < SUPER. Every group names its minimum role,
		// so a token with an unknown or empty role reaches nothing.

		// ── Catalog reads (USER+) ─────────────────────────────────
		// ADR-0001: may continue under the signed token while UM is unreachable.
		r.Group(func(r chi.Router) {
			r.Use(live.RequireOrDegrade())
			r.Use(mw.RequireRole(RoleUSER))

			r.Get("/drugs", dh.List)
			r.Get("/drugs/low-stock", dh.LowStock)
			r.Get("/drugs/{id}/lots", lh.ListLots)
			r.Get("/lots/expiring", lh.Expiring)
		})

		// Everything below is a write or sensitive read and needs a current
		// UM session (ADR-0001).
		r.Group(func(r chi.Router) {
			r.Use(live.Require())

			// ── USER+: selling ────────────────────────────────────
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireRole(RoleUSER))
				// Customers (look up + add at the counter)
				r.Get("/customers", ch.List)
				r.Post("/customers", ch.Add)
				r.Get("/customers/{id}/sales", ch.GetSales)

				// Sales (create + view + return against a bill)
				r.Get("/sales", sh.List)
				r.Post("/sales", sh.Create)
				r.Get("/sales/{id}/items", sh.Items)
				r.Get("/sales/{id}/ky", kh.BySale)
				r.Post("/sales/{id}/return", reth.Create)
				r.Get("/sales/{id}/returns", reth.List)

				// Movements (read-only audit)
				r.Get("/movements", mvh.List)

				// Settings (read — receipts need store info)
				r.Get("/settings", seth.Get)
			})

			// ── MANAGER+: stock operations, goods receipt, customers ──
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireRole(RoleMANAGER))

				// Stock counts, adjustments, and lots
				r.Get("/drugs/reorder-suggestions", dh.ReorderSuggestions)
				r.Post("/drugs/{id}/adjustments", ah.Create)
				r.Get("/drugs/{id}/adjustments", ah.List)
				r.Get("/stock-counts", sch.List)
				r.Post("/stock-counts", sch.Create)
				r.Post("/drugs/{id}/lots", lh.AddLot)
				r.Delete("/drugs/{id}/lots/{lot_id}", lh.DeleteLot)
				r.Post("/lots/writeoff", lh.WriteoffLots)

				// Goods receipt (a lot's sell_price override does not change
				// the drug's selling price used at checkout)
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

				// Customer profile edit
				r.Put("/customers/{id}", ch.Update)

				// Label print (barcode/price labels — returns PDF)
				r.Post("/labels/print", labh.Print)

				// Operational stock report
				r.Get("/report/slow-drugs", rh.SlowDrugs)
			})

			// ── ADMIN+: drug identity and price, void, KY, settings, reports ──
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireRole(RoleADMIN))

				// Drug identity and selling price
				r.Post("/drugs", dh.Add)
				r.Post("/drugs/bulk", dh.BulkImport)
				r.Put("/drugs/{id}", dh.Update)

				// Whole-bill void
				r.Post("/sales/{id}/void", sh.Void)

				// Financial and customer-identifying reports
				r.Get("/report/summary", rh.Summary)
				r.Get("/report/dashboard", rh.Dashboard)
				r.Get("/report/daily", rh.Daily)
				r.Get("/report/monthly", rh.Monthly)
				r.Get("/report/top-drugs", rh.TopDrugs)
				r.Get("/report/eod", rh.Eod)
				r.Get("/report/profit", rh.Profit)

				// KY administration and export
				r.Get("/ky9", kh.ListKy9)
				r.Post("/ky9", kh.AddKy9)
				r.Get("/ky10", kh.ListKy10)
				r.Post("/ky10", kh.AddKy10)
				r.Get("/ky11", kh.ListKy11)
				r.Post("/ky11", kh.AddKy11)
				r.Get("/ky12", kh.ListKy12)
				r.Post("/ky12", kh.AddKy12)
				r.Get("/export/{form}", eh.Export)

				// Settings (write)
				r.Put("/settings", seth.Update)
			})
		})
	})

	return r
}
