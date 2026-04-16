package db

import (
	"context"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"pharmacy-pos/backend/models"
)

type MongoDB struct {
	client *mongo.Client
	db     *mongo.Database
}

func Connect(uri, dbName string) *MongoDB {
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
	log.Println("Connected to MongoDB")
	return &MongoDB{client: client, db: client.Database(dbName)}
}

func (m *MongoDB) Drugs() *mongo.Collection     { return m.db.Collection("drugs") }
func (m *MongoDB) DrugLots() *mongo.Collection  { return m.db.Collection("drug_lots") }
func (m *MongoDB) Customers() *mongo.Collection { return m.db.Collection("customers") }
func (m *MongoDB) Sales() *mongo.Collection     { return m.db.Collection("sales") }
func (m *MongoDB) SaleItems() *mongo.Collection { return m.db.Collection("sale_items") }
func (m *MongoDB) Counters() *mongo.Collection  { return m.db.Collection("counters") }
func (m *MongoDB) Ky9() *mongo.Collection       { return m.db.Collection("ky9") }
func (m *MongoDB) Ky10() *mongo.Collection      { return m.db.Collection("ky10") }
func (m *MongoDB) Ky11() *mongo.Collection      { return m.db.Collection("ky11") }
func (m *MongoDB) Ky12() *mongo.Collection           { return m.db.Collection("ky12") }
func (m *MongoDB) PurchaseOrders() *mongo.Collection { return m.db.Collection("purchase_orders") }
func (m *MongoDB) Suppliers() *mongo.Collection         { return m.db.Collection("suppliers") }
func (m *MongoDB) StockAdjustments() *mongo.Collection { return m.db.Collection("stock_adjustments") }
func (m *MongoDB) DrugReturns() *mongo.Collection      { return m.db.Collection("drug_returns") }

func (m *MongoDB) CreateIndexes(ctx context.Context) {
	// Unique index on sales.bill_no
	m.Sales().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "bill_no", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	// Index on sale_items.sale_id
	m.SaleItems().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "sale_id", Value: 1}},
	})
	// Index on ky forms date
	for _, col := range []*mongo.Collection{m.Ky9(), m.Ky10(), m.Ky11(), m.Ky12()} {
		col.Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: bson.D{{Key: "date", Value: 1}},
		})
	}
	// Indexes on drug_lots
	m.DrugLots().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "drug_id", Value: 1}},
	})
	m.DrugLots().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "expiry_date", Value: 1}},
	})
	// Indexes on purchase_orders
	m.PurchaseOrders().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "created_at", Value: -1}},
	})
	m.PurchaseOrders().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "doc_no", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	// Unique index on suppliers.name
	m.Suppliers().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "name", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	// Compound index on stock_adjustments: drug_id + created_at DESC
	m.StockAdjustments().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "drug_id", Value: 1}, {Key: "created_at", Value: -1}},
	})
	// Index on drug_returns.sale_id
	m.DrugReturns().Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "sale_id", Value: 1}},
	})
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
