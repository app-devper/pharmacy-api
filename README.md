# Pharmacy API

REST API สำหรับระบบจัดการร้านขายยา — จัดการยา, สต็อก, การขาย, ลูกค้า, รายงาน และแบบฟอร์ม ขย.

---

## Tech Stack

![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat-square&logo=go&logoColor=white)
![Chi](https://img.shields.io/badge/Chi_Router-v5-00ADD8?style=flat-square&logo=go&logoColor=white)
![MongoDB](https://img.shields.io/badge/MongoDB-v2_Driver-47A248?style=flat-square&logo=mongodb&logoColor=white)
![JWT](https://img.shields.io/badge/JWT-Auth-000000?style=flat-square&logo=jsonwebtokens&logoColor=white)

| Component | Detail |
|-----------|--------|
| Language | Go 1.25 |
| Router | Chi v5 |
| Database | MongoDB (Driver v2) |
| Auth | JWT (HS256) — verify token จาก Um-Api |

---

## โครงสร้างโปรเจกต์

```
backend/
├── config/         # โหลด environment variables
├── db/             # MongoDB connection + indexes + seed data
├── handlers/       # HTTP handlers
│   ├── drugs.go
│   ├── drug_lots.go
│   ├── imports.go
│   ├── sales.go
│   ├── customers.go
│   ├── suppliers.go
│   ├── stock_adjustments.go
│   ├── drug_returns.go
│   ├── report.go
│   ├── kyforms.go
│   ├── export.go
│   └── helpers.go
├── middleware/      # CORS, JWT Auth, RBAC
│   ├── cors.go
│   ├── auth.go
│   └── authorize.go
├── models/         # Go structs (bson + json tags)
├── routes/         # Chi router setup
├── pdf/            # PDF generation (Thai font)
├── .env            # Environment variables (ไม่ commit)
├── go.mod
└── main.go
```

---

## วิธีติดตั้งและรัน

### ข้อกำหนด

- Go 1.25+
- MongoDB (local หรือ Atlas)

### ตั้งค่า Environment

สร้างไฟล์ `.env`:

```env
MONGO_URI=mongodb://localhost:27017
DB_NAME=pharmacy
PORT=8087
FRONTEND_ORIGIN=http://localhost:5173
SECRET_KEY=your_jwt_secret_key
UM_API_URL=http://localhost:8585
```

> **SECRET_KEY** ต้องตรงกับค่าที่ใช้ใน Um-Api เพื่อ verify JWT token

### รัน

```bash
go mod download
go run main.go
# → http://localhost:8087
```

---

## Authentication

API ทุก endpoint ใต้ `/api/*` ต้องส่ง JWT token ใน header:

```
Authorization: Bearer <token>
```

- Token ได้จาก **Um-Api** (`POST /api/um/v1/auth/login`)
- Backend verify token locally ด้วย shared `SECRET_KEY` (HS256)
- JWT claims: `role` (SUPER/ADMIN/USER), `system`, `clientId`, `sessionId`

---

## API Reference

### Drugs

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/drugs` | รายการยาทั้งหมด |
| `GET` | `/api/drugs/low-stock` | ยาที่สต็อกต่ำ |
| `POST` | `/api/drugs` | เพิ่มยาใหม่ |
| `PUT` | `/api/drugs/:id` | แก้ไขข้อมูลยา |

### Stock Adjustments

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `POST` | `/api/drugs/:id/adjustments` | ปรับสต็อก (audit log) |
| `GET` | `/api/drugs/:id/adjustments` | ประวัติการปรับสต็อก |

### Drug Lots (FEFO)

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/drugs/:id/lots` | รายการล็อต (เรียง expiry ASC) |
| `POST` | `/api/drugs/:id/lots` | เพิ่มล็อต → `$inc stock` |
| `DELETE` | `/api/drugs/:id/lots/:lot_id` | ลบล็อต → `$dec stock` ตาม remaining |
| `GET` | `/api/lots/expiring?days=N` | ล็อตที่หมดอายุหรือจะหมดใน N วัน (default 60) |
| `POST` | `/api/lots/writeoff` | ตัดจำหน่ายล็อตหมดอายุ |

### Sales

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `POST` | `/api/sales` | บันทึกการขาย (FEFO deduction) |
| `GET` | `/api/sales` | ประวัติการขาย |
| `GET` | `/api/sales/:id/items` | รายการสินค้าในใบขาย |
| `POST` | `/api/sales/:id/void` | ยกเลิกใบขาย |
| `POST` | `/api/sales/:id/return` | คืนสินค้า |
| `GET` | `/api/sales/:id/returns` | ประวัติการคืนสินค้า |

### Customers

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/customers` | รายการลูกค้า |
| `POST` | `/api/customers` | เพิ่มลูกค้า |
| `PUT` | `/api/customers/:id` | แก้ไขข้อมูลลูกค้า |
| `GET` | `/api/customers/:id/sales` | ประวัติการซื้อของลูกค้า |

### Imports (Purchase Orders)

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/imports` | รายการใบนำเข้า |
| `POST` | `/api/imports` | สร้างใบนำเข้าใหม่ (status: draft) |
| `GET` | `/api/imports/:id` | รายละเอียดพร้อม items |
| `PUT` | `/api/imports/:id` | แก้ไข (draft เท่านั้น) |
| `POST` | `/api/imports/:id/confirm` | ยืนยัน → สร้าง DrugLots + เพิ่มสต็อก |
| `DELETE` | `/api/imports/:id` | ลบ (draft เท่านั้น) |

### Suppliers

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/suppliers` | รายการซัพพลายเออร์ |
| `POST` | `/api/suppliers` | เพิ่มซัพพลายเออร์ |
| `PUT` | `/api/suppliers/:id` | แก้ไข |
| `DELETE` | `/api/suppliers/:id` | ลบ |

### Reports

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/report/summary` | สรุปภาพรวม |
| `GET` | `/api/report/daily` | ยอดขายรายวัน |
| `GET` | `/api/report/eod` | สรุปปิดยอดประจำวัน |
| `GET` | `/api/report/profit` | รายงานกำไร |
| `GET` | `/api/report/top-drugs` | ยาขายดี |
| `GET` | `/api/report/slow-drugs` | ยาขายช้า |
| `GET` | `/api/report/monthly` | สรุปรายเดือน |

### KY Forms & PDF Export

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET/POST` | `/api/ky9` | แบบ ขย.9 |
| `GET/POST` | `/api/ky10` | แบบ ขย.10 |
| `GET/POST` | `/api/ky11` | แบบ ขย.11 |
| `GET/POST` | `/api/ky12` | แบบ ขย.12 |
| `GET` | `/api/export/:form` | Export PDF (ky9, ky10, ky11, ky12) |

---

## Data Models

### Drug

```go
type Drug struct {
    ID          bson.ObjectID  // id
    Name        string         // ชื่อการค้า
    GenericName string         // ชื่อสามัญ
    Type        string         // ประเภทยา
    Strength    string         // ขนาดยา เช่น "500mg"
    Barcode     string         // บาร์โค้ด
    SellPrice   float64        // ราคาขาย (bson:"price" สำหรับ backward compat)
    CostPrice   float64        // ราคาทุน
    Stock       int            // จำนวนคงเหลือรวม
    MinStock    int            // จุดสั่งซื้อขั้นต่ำ
    RegNo       string         // เลขทะเบียนยา
    Unit        string         // หน่วย เช่น "เม็ด", "แคปซูล"
    ReportTypes []string       // ["ky9", "ky10", "ky11", "ky12"]
    CreatedAt   time.Time      // วันที่สร้าง
}
```

### Drug Lot (FEFO)

ระบบใช้ **FEFO (First Expired, First Out)** — ตัดสต็อกจากล็อตที่ใกล้หมดอายุก่อนเสมอ

```go
type DrugLot struct {
    ID         bson.ObjectID  // id
    DrugID     bson.ObjectID  // อ้างอิงยา
    LotNumber  string         // หมายเลขล็อต
    ExpiryDate time.Time      // วันหมดอายุ (เรียงตามนี้)
    ImportDate time.Time      // วันที่นำเข้า
    CostPrice  *float64       // nil = ใช้ drug.CostPrice
    SellPrice  *float64       // nil = ใช้ drug.SellPrice
    Quantity   int            // จำนวนที่นำเข้า (immutable)
    Remaining  int            // คงเหลือในล็อตนี้ (ลดเมื่อขาย)
    CreatedAt  time.Time      // วันที่สร้าง
}
```

### Sale

เลขที่บิลสร้างอัตโนมัติ

```go
type Sale struct {
    ID           bson.ObjectID   // id
    BillNo       string          // เลขที่บิล
    CustomerID   *bson.ObjectID  // ลูกค้า (nullable)
    CustomerName string          // ชื่อลูกค้า
    Discount     float64         // ส่วนลด
    Total        float64         // ยอดรวมหลังลด
    Received     float64         // เงินรับ
    Change       float64         // เงินทอน
    SoldAt       time.Time       // วันที่ขาย
    Voided       bool            // ยกเลิกแล้วหรือไม่
    VoidReason   string          // เหตุผลยกเลิก
    VoidedAt     *time.Time      // วันที่ยกเลิก
}

type SaleItem struct {
    ID       bson.ObjectID  // id
    SaleID   bson.ObjectID  // อ้างอิงบิล
    DrugID   bson.ObjectID  // อ้างอิงยา
    DrugName string         // ชื่อยา
    Qty      int            // จำนวน
    Price    float64        // ราคาต่อหน่วย
    Subtotal float64        // รวม
}
```

### Customer

```go
type Customer struct {
    ID         bson.ObjectID  // id
    Name       string         // ชื่อ
    Phone      string         // เบอร์โทร
    Disease    string         // โรคประจำตัว
    TotalSpent float64        // ยอดซื้อสะสม
    LastVisit  *time.Time     // เข้าร้านล่าสุด
    CreatedAt  time.Time      // วันที่สร้าง
}
```

### Supplier

```go
type Supplier struct {
    ID          bson.ObjectID  // id
    Name        string         // ชื่อบริษัท
    ContactName string         // ชื่อผู้ติดต่อ
    Phone       string         // เบอร์โทร
    Address     string         // ที่อยู่
    TaxID       string         // เลขประจำตัวผู้เสียภาษี
    Notes       string         // หมายเหตุ
    CreatedAt   time.Time      // วันที่สร้าง
}
```

### Stock Adjustment

Audit log สำหรับการปรับสต็อก เหตุผลที่รองรับ: `นับสต็อก`, `ยาเสียหาย`, `ยาหมดอายุ`, `สูญหาย`, `อื่นๆ`

```go
type StockAdjustment struct {
    ID        bson.ObjectID  // id
    DrugID    bson.ObjectID  // อ้างอิงยา
    DrugName  string         // ชื่อยา
    Delta     int            // จำนวนที่เปลี่ยน (+/-)
    Before    int            // สต็อกก่อนปรับ
    After     int            // สต็อกหลังปรับ
    Reason    string         // เหตุผล
    Note      string         // หมายเหตุ
    CreatedAt time.Time      // วันที่สร้าง
}
```

### Drug Return

เลขที่คืนสินค้าสร้างอัตโนมัติ

```go
type DrugReturn struct {
    ID           bson.ObjectID   // id
    ReturnNo     string          // เลขที่ใบคืน
    SaleID       bson.ObjectID   // อ้างอิงบิลขาย
    BillNo       string          // เลขที่บิลขาย
    CustomerID   *bson.ObjectID  // ลูกค้า (nullable)
    CustomerName string          // ชื่อลูกค้า
    Items        []ReturnItem    // รายการคืน
    Refund       float64         // ยอดคืนเงิน
    Reason       string          // เหตุผล
    ReturnedAt   time.Time       // วันที่คืน
}

type ReturnItem struct {
    SaleItemID bson.ObjectID  // อ้างอิง SaleItem
    DrugID     bson.ObjectID  // อ้างอิงยา
    DrugName   string         // ชื่อยา
    Qty        int            // จำนวนคืน
    Price      float64        // ราคาต่อหน่วย
    Subtotal   float64        // รวม
}
```

### Purchase Order (Import)

เลขที่เอกสารสร้างอัตโนมัติในรูปแบบ `IMP-YYMMDD-NNN`

```
IMP-260415-001   ← ใบนำเข้าที่ 1 ของวันที่ 15 เม.ย. 2026
IMP-260415-002   ← ใบนำเข้าที่ 2 ของวันเดียวกัน
```

**สถานะ:** `draft` → (ยืนยัน) → `confirmed`
เมื่อยืนยัน: สร้าง `DrugLot` ต่อรายการ + `$inc drug.stock` ทันที

```go
type PurchaseOrder struct {
    ID          bson.ObjectID  // id
    DocNo       string         // เลขที่เอกสาร (IMP-YYMMDD-NNN)
    Supplier    string         // ซัพพลายเออร์
    InvoiceNo   string         // เลขที่ใบแจ้งหนี้
    ReceiveDate time.Time      // วันที่รับสินค้า
    Items       []POItem       // รายการสินค้า
    ItemCount   int            // จำนวนรายการ (denormalized)
    TotalCost   float64        // รวมต้นทุน sum(qty*cost_price)
    Status      string         // "draft" | "confirmed"
    Notes       string         // หมายเหตุ
    CreatedAt   time.Time      // วันที่สร้าง
    ConfirmedAt *time.Time     // วันที่ยืนยัน
}

type POItem struct {
    DrugID     bson.ObjectID  // อ้างอิงยา
    DrugName   string         // ชื่อยา
    LotNumber  string         // หมายเลขล็อต
    ExpiryDate string         // วันหมดอายุ "YYYY-MM-DD"
    Qty        int            // จำนวน
    CostPrice  float64        // ราคาทุน
    SellPrice  *float64       // ราคาขาย (nil = ใช้ค่า default)
}
```

### Report Types

```go
type ReportSummary struct {
    TodaySales float64  // ยอดขายวันนี้
    TodayBills int      // จำนวนบิลวันนี้
    MonthSales float64  // ยอดขายเดือนนี้
    StockValue float64  // มูลค่าสต็อก
    LowStock   int      // จำนวนยาที่สต็อกต่ำ
    OutStock   int      // จำนวนยาที่หมด
}

type EodReport struct {
    Date          string   // YYYY-MM-DD
    BillCount     int      // จำนวนบิล
    TotalSales    float64  // ยอดขายรวม (หลังลด)
    TotalDiscount float64  // ส่วนลดรวม
    TotalReceived float64  // เงินรับรวม
    TotalChange   float64  // เงินทอนรวม
    NetCash       float64  // เงินสดสุทธิ
    Bills         []Sale   // รายการบิล
}

type ProfitReport struct {
    Summary ProfitSummary  // สรุปกำไร (revenue, cost, profit, margin, bills)
    ByDrug  []DrugProfit   // กำไรแยกตามยา
}

type MonthlyData struct {
    Month   string   // "YYYY-MM"
    Revenue float64  // รายได้
    Cost    float64  // ต้นทุน
    Profit  float64  // กำไร
}
```
