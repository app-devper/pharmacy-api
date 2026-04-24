package db

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/models"
)

// validClientID allows only alphanumeric, underscore, and hyphen characters.
var validClientID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// MongoDB wraps a single database instance and its collections.
type MongoDB struct {
	client               *mongo.Client
	db                   *mongo.Database
	supportsTransactions bool
}

type tenantDB struct {
	db       *MongoDB
	initOnce sync.Once
}

// Manager holds one mongo.Client and a per-clientId database cache.
// DB name: clientId "000" → dbPrefix (e.g. "pharmacy"); others → "<dbPrefix>_<clientId>" (e.g. "pharmacy_abc").
type Manager struct {
	client               *mongo.Client
	dbPrefix             string
	supportsTransactions bool
	cache                sync.Map // map[clientId string]*tenantDB
}

// NewManager connects to MongoDB and returns a Manager.
// Use Manager.ForClient(clientId) to get a per-tenant *MongoDB.
func NewManager(uri, dbPrefix string) *Manager {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bsonOpts := &options.BSONOptions{ObjectIDAsHexString: true}
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetBSONOptions(bsonOpts))
	if err != nil {
		log.Fatalf("MongoDB connect error: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("MongoDB ping error: %v", err)
	}

	// Detect transaction support using the default DB
	probe := client.Database(dbPrefix)
	supportsTransactions, err := detectTransactionSupport(ctx, probe)
	if err != nil {
		log.Printf("MongoDB transaction capability check failed: %v", err)
	}
	log.Println("Connected to MongoDB")
	if !supportsTransactions {
		log.Println("MongoDB transactions unavailable; falling back to non-transactional writes")
	}
	return &Manager{
		client:               client,
		dbPrefix:             dbPrefix,
		supportsTransactions: supportsTransactions,
	}
}

// ForClient returns (and caches) a *MongoDB for the given clientId.
// Special case: clientId "000" → database = dbPrefix (e.g. "pharmacy")
// All other clientIds          → database = "<dbPrefix>_<clientId>" (e.g. "pharmacy_abc")
// Returns an error if clientID is empty or contains illegal characters.
func (m *Manager) ForClient(clientID string) (*MongoDB, error) {
	if clientID == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	if clientID != "000" && !validClientID.MatchString(clientID) {
		return nil, fmt.Errorf("invalid clientId %q", clientID)
	}

	if v, ok := m.cache.Load(clientID); ok {
		entry := v.(*tenantDB)
		entry.ensureInitialized(clientID)
		return entry.db, nil
	}

	var dbName string
	if clientID == "000" {
		dbName = m.dbPrefix // "pharmacy"
	} else {
		dbName = fmt.Sprintf("%s_%s", m.dbPrefix, clientID) // "pharmacy_abc"
	}
	d := &MongoDB{
		client:               m.client,
		db:                   m.client.Database(dbName),
		supportsTransactions: m.supportsTransactions,
	}
	entry := &tenantDB{db: d}
	actual, _ := m.cache.LoadOrStore(clientID, entry)
	entry = actual.(*tenantDB)
	entry.ensureInitialized(clientID)
	return entry.db, nil
}

// forClientNoInit returns a *MongoDB without running tenant initialization.
// Used internally for bootstrap paths that manage initialization explicitly.
func (m *Manager) forClientNoInit(clientID string) (*MongoDB, error) {
	if clientID == "" {
		return nil, fmt.Errorf("clientId is required")
	}
	if clientID != "000" && !validClientID.MatchString(clientID) {
		return nil, fmt.Errorf("invalid clientId %q", clientID)
	}
	var dbName string
	if clientID == "000" {
		dbName = m.dbPrefix
	} else {
		dbName = fmt.Sprintf("%s_%s", m.dbPrefix, clientID)
	}
	return &MongoDB{
		client:               m.client,
		db:                   m.client.Database(dbName),
		supportsTransactions: m.supportsTransactions,
	}, nil
}

// CreateIndexesForClient creates indexes on the given clientId's database.
// Returns an error so callers (e.g. bootstrap) can fail fast.
func (m *Manager) CreateIndexesForClient(ctx context.Context, clientID string) error {
	d, err := m.forClientNoInit(clientID)
	if err != nil {
		return err
	}
	if err := d.CreateIndexes(ctx); err != nil {
		return err
	}
	// Mark the tenant as initialized in the cache so request-path callers
	// don't re-run CreateIndexes.
	entry := &tenantDB{db: d}
	entry.initOnce.Do(func() {
		log.Printf("Opened database %q for client %q", d.db.Name(), clientID)
	})
	m.cache.LoadOrStore(clientID, entry)
	return nil
}

// SeedForClient seeds initial data on the given clientId's database (no-op if already seeded).
func (m *Manager) SeedForClient(ctx context.Context, clientID string) error {
	d, err := m.forClientNoInit(clientID)
	if err != nil {
		return err
	}
	d.Seed(ctx)
	return nil
}

func (m *MongoDB) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	if !m.supportsTransactions {
		return fmt.Errorf("MongoDB transactions are required for this operation; run MongoDB as a replica set or sharded cluster")
	}

	sess, err := m.client.StartSession()
	if err != nil {
		return err
	}
	defer sess.EndSession(ctx)

	_, err = sess.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		return nil, fn(txCtx)
	})
	return err
}

func detectTransactionSupport(ctx context.Context, database *mongo.Database) (bool, error) {
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := database.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return false, err
	}
	return hello.SetName != "" || hello.Msg == "isdbgrid", nil
}

func (m *MongoDB) Drugs() *mongo.Collection            { return m.db.Collection("drugs") }
func (m *MongoDB) DrugLots() *mongo.Collection         { return m.db.Collection("drug_lots") }
func (m *MongoDB) Customers() *mongo.Collection        { return m.db.Collection("customers") }
func (m *MongoDB) Sales() *mongo.Collection            { return m.db.Collection("sales") }
func (m *MongoDB) SaleItems() *mongo.Collection        { return m.db.Collection("sale_items") }
func (m *MongoDB) Counters() *mongo.Collection         { return m.db.Collection("counters") }
func (m *MongoDB) Ky9() *mongo.Collection              { return m.db.Collection("ky9") }
func (m *MongoDB) Ky10() *mongo.Collection             { return m.db.Collection("ky10") }
func (m *MongoDB) Ky11() *mongo.Collection             { return m.db.Collection("ky11") }
func (m *MongoDB) Ky12() *mongo.Collection             { return m.db.Collection("ky12") }
func (m *MongoDB) PurchaseOrders() *mongo.Collection   { return m.db.Collection("purchase_orders") }
func (m *MongoDB) Suppliers() *mongo.Collection        { return m.db.Collection("suppliers") }
func (m *MongoDB) StockAdjustments() *mongo.Collection { return m.db.Collection("stock_adjustments") }
func (m *MongoDB) StockCounts() *mongo.Collection      { return m.db.Collection("stock_counts") }
func (m *MongoDB) DrugReturns() *mongo.Collection      { return m.db.Collection("drug_returns") }
func (m *MongoDB) LotWriteoffs() *mongo.Collection     { return m.db.Collection("lot_writeoffs") }
func (m *MongoDB) Settings() *mongo.Collection         { return m.db.Collection("settings") }

// ensureInitialized runs index creation for a tenant exactly once, best-effort.
// Errors are logged but never propagated, so a single bad index (e.g. a unique
// constraint violation on legacy data) cannot block the tenant's API.
// Bootstrap paths that want fail-fast behavior should call Manager.CreateIndexesForClient instead.
func (t *tenantDB) ensureInitialized(clientID string) {
	t.initOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := t.db.CreateIndexes(ctx); err != nil {
			log.Printf("tenant %q: index creation reported error (continuing): %v", clientID, err)
		}
		log.Printf("Opened database %q for client %q", t.db.db.Name(), clientID)
	})
}

func (m *MongoDB) CreateIndexes(ctx context.Context) error {
	// Unique index on sales.bill_no
	if _, err := m.Sales().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "bill_no", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	if _, err := m.Sales().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "client_request_id", Value: 1}},
		Options: options.Index().SetUnique(true).SetPartialFilterExpression(
			bson.M{"client_request_id": bson.M{"$type": "string", "$gt": ""}},
		),
	}); err != nil {
		return err
	}
	// Index on sale_items.sale_id
	if _, err := m.SaleItems().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "sale_id", Value: 1}},
	}); err != nil {
		return err
	}
	// Index on ky forms date
	for _, col := range []*mongo.Collection{m.Ky9(), m.Ky10(), m.Ky11(), m.Ky12()} {
		if _, err := col.Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: bson.D{{Key: "date", Value: 1}},
		}); err != nil {
			return err
		}
	}
	// Indexes on drug_lots
	if _, err := m.DrugLots().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "drug_id", Value: 1}},
	}); err != nil {
		return err
	}
	if _, err := m.DrugLots().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "expiry_date", Value: 1}},
	}); err != nil {
		return err
	}
	if _, err := m.DrugLots().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "import_date", Value: 1}},
	}); err != nil {
		return err
	}
	// Indexes on purchase_orders
	if _, err := m.PurchaseOrders().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "created_at", Value: -1}},
	}); err != nil {
		return err
	}
	if _, err := m.PurchaseOrders().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "doc_no", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	// Unique index on suppliers.name
	if _, err := m.Suppliers().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "name", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	// Compound index on stock_adjustments: drug_id + created_at DESC
	if _, err := m.StockAdjustments().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "drug_id", Value: 1}, {Key: "created_at", Value: -1}},
	}); err != nil {
		return err
	}
	if _, err := m.StockCounts().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "count_no", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	if _, err := m.StockCounts().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "created_at", Value: -1}},
	}); err != nil {
		return err
	}
	// Index on drug_returns.sale_id
	if _, err := m.DrugReturns().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "sale_id", Value: 1}},
	}); err != nil {
		return err
	}
	// Unique index on settings.key — guarantees the singleton row cannot be duplicated.
	if _, err := m.Settings().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "key", Value: 1}},
		Options: options.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	// Partial unique index on customers.phone — prevent duplicate phone numbers while
	// still allowing multiple customers without a phone.
	if _, err := m.Customers().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "phone", Value: 1}},
		Options: options.Index().SetUnique(true).SetPartialFilterExpression(
			bson.M{"phone": bson.M{"$type": "string", "$gt": ""}},
		),
	}); err != nil {
		return err
	}
	// Partial unique index on drugs.barcode — only enforced for non-empty string barcodes.
	// A plain sparse index would still index empty strings (the field is always present
	// because the bson tag has no omitempty), causing duplicate-key errors for multiple
	// drugs with no barcode. A partial filter correctly excludes "" values.
	if _, err := m.Drugs().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "barcode", Value: 1}},
		Options: options.Index().SetUnique(true).SetPartialFilterExpression(
			bson.M{"barcode": bson.M{"$type": "string", "$gt": ""}},
		),
	}); err != nil {
		return err
	}
	return nil
}

func (m *MongoDB) Seed(ctx context.Context) {
	count, _ := m.Drugs().CountDocuments(ctx, bson.M{})
	if count > 0 {
		return
	}
	log.Println("Seeding initial data...")

	now := time.Now()
	drugs := []interface{}{
		models.Drug{Name: "พาราเซตามอล 500mg", GenericName: "Paracetamol", Type: "ยาสามัญ", Strength: "500mg", SellPrice: 5, CostPrice: 2, Stock: 200, RegNo: "1A 12/53", Unit: "เม็ด", ReportTypes: []string{"ky9"}, CreatedAt: now},
		models.Drug{Name: "แอสไพริน 300mg", GenericName: "Aspirin", Type: "ยาสามัญ", Strength: "300mg", SellPrice: 8, CostPrice: 3, Stock: 150, RegNo: "1A 15/53", Unit: "เม็ด", ReportTypes: []string{"ky9"}, CreatedAt: now},
		models.Drug{Name: "ไอบูโพรเฟน 200mg", GenericName: "Ibuprofen", Type: "ยาแผนปัจจุบัน", Strength: "200mg", SellPrice: 12, CostPrice: 5, Stock: 100, RegNo: "2B 20/54", Unit: "เม็ด", ReportTypes: []string{"ky9", "ky11"}, CreatedAt: now},
		models.Drug{Name: "อะม็อกซีซิลลิน 250mg", GenericName: "Amoxicillin", Type: "ยาแผนปัจจุบัน", Strength: "250mg", SellPrice: 25, CostPrice: 12, Stock: 80, RegNo: "2B 30/54", Unit: "แคปซูล", ReportTypes: []string{"ky9", "ky10", "ky12"}, CreatedAt: now},
		models.Drug{Name: "วิตามินซี 1000mg", GenericName: "Vitamin C", Type: "อาหารเสริม", Strength: "1000mg", SellPrice: 15, CostPrice: 6, Stock: 300, RegNo: "", Unit: "เม็ด", ReportTypes: []string{}, CreatedAt: now},
		models.Drug{Name: "ยาแก้ไอสมุนไพร", GenericName: "", Type: "ยาสมุนไพร", Strength: "", SellPrice: 35, CostPrice: 15, Stock: 50, RegNo: "G 100/55", Unit: "ขวด", ReportTypes: []string{"ky9"}, CreatedAt: now},
		models.Drug{Name: "ยาลดกรด", GenericName: "Antacid", Type: "ยาสามัญ", Strength: "", SellPrice: 10, CostPrice: 4, Stock: 120, RegNo: "1A 40/56", Unit: "เม็ด", ReportTypes: []string{"ky9"}, CreatedAt: now},
		models.Drug{Name: "ยาแก้แพ้ คลอร์เฟนิรามีน", GenericName: "Chlorpheniramine", Type: "ยาแผนปัจจุบัน", Strength: "4mg", SellPrice: 6, CostPrice: 2, Stock: 180, RegNo: "2B 55/56", Unit: "เม็ด", ReportTypes: []string{"ky9", "ky11"}, CreatedAt: now},
		models.Drug{Name: "น้ำเกลือล้างแผล", GenericName: "Normal Saline", Type: "ยาสามัญ", Strength: "0.9%", SellPrice: 20, CostPrice: 8, Stock: 60, RegNo: "", Unit: "ขวด", ReportTypes: []string{"ky9"}, CreatedAt: now},
		models.Drug{Name: "ยาดม สหพัฒน์", GenericName: "", Type: "ยาสมุนไพร", Strength: "", SellPrice: 45, CostPrice: 20, Stock: 15, RegNo: "G 200/55", Unit: "กล่อง", ReportTypes: []string{}, CreatedAt: now},
	}
	m.Drugs().InsertMany(ctx, drugs)

	customers := []interface{}{
		models.Customer{Name: "สมชาย ใจดี", Phone: "0812345678", Disease: "เบาหวาน", TotalSpent: 0, CreatedAt: now},
		models.Customer{Name: "สมหญิง รักสุขภาพ", Phone: "0898765432", Disease: "แพ้เพนิซิลิน", TotalSpent: 0, CreatedAt: now},
		models.Customer{Name: "วิชัย สุขใจ", Phone: "0856781234", Disease: "-", TotalSpent: 0, CreatedAt: now},
	}
	m.Customers().InsertMany(ctx, customers)
	log.Println("Seed complete")
}
