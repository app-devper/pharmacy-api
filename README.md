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
│   ├── movements.go
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
- JWT claims: `role` (SUPER/ADMIN/USER), `system`, `clientId`, `sessionId`, `exp`
- Environment ที่ต้องตั้งเพิ่ม: `SYSTEM`

---

## API Reference

### Drugs

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/drugs` | รายการยาทั้งหมด (รองรับ `?fields=compact`) |
| `GET` | `/api/drugs/low-stock` | ยาที่สต็อกต่ำ (threshold = `min_stock` หรือ `20` ถ้าไม่ตั้ง) |
| `POST` | `/api/drugs` | เพิ่มยาใหม่ (ต้องมี `create_lot` เมื่อ `stock > 0`) |
| `PUT` | `/api/drugs/:id` | แก้ไขข้อมูลยา |
| `POST` | `/api/drugs/bulk` | นำเข้ายาจาก Excel สูงสุด 1,000 รายการ |
| `GET` | `/api/drugs/reorder-suggestions` | แนะนำสั่งซื้อ (ADMIN) |

**Projection** — `GET /api/drugs?fields=compact` คืนเฉพาะ `id, name, price, cost_price, stock, barcode, reg_no, unit, report_types` เพื่อลด payload สำหรับ client ที่ไม่ต้องการข้อมูลเต็ม

**POST /api/drugs** — ถ้า `stock > 0` ต้องส่ง `create_lot: { lot_number, expiry_date, quantity, ... }` ไปพร้อมกัน → backend สร้าง Drug + DrugLot ใน transaction เดียวกัน · `create_lot.quantity` เป็นค่าที่ใช้เป็น `drug.stock` · `expiry_date` ต้องอยู่หลังวันนี้

**Bulk Import** — Request: `{ "drugs": [DrugInput, ...] }` · Response: `{ "imported": N, "errors": [{ "row": N, "name": "...", "message": "..." }] }`
ข้อผิดพลาดต่อแถวไม่หยุด batch — รายการที่สำเร็จถูก insert ต่อไป · `stock < 0` จะถูก clamp เป็น `0` · `stock > 0` ยอมรับได้โดยไม่ต้องมี lot (ต่างจาก `POST /api/drugs` ที่เข้มงวดกว่า)

**Reorder Suggestions** — `GET /api/drugs/reorder-suggestions?days=30&lookahead=14` **sales-driven** — พิจารณาเฉพาะยาที่มีการขายในช่วง `days` เท่านั้น (ยาที่ขายช้า/ไม่เคยขายจะถูกข้าม ไม่ว่า `min_stock` จะเป็นเท่าไหร่) · คำนวณ `avg_daily_sale = qty_sold / days` → `projected_need = ceil(avg_daily × lookahead)` · คืนยาที่ `current_stock < projected_need` พร้อม `suggested_qty = projected_need − current_stock` · เรียง out-of-stock ก่อน แล้วตาม `days_left` น้อยสุด

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
| `GET` | `/api/lots/expiring?expired_only=true` | เฉพาะล็อตที่ผ่านวันหมดอายุแล้ว (remaining > 0) |
| `POST` | `/api/lots/writeoff` | ตัดจำหน่าย bulk ล็อตหมดอายุ → `$dec stock` ต่อล็อต |

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
| `GET` | `/api/report/dashboard` | รวม summary + daily + monthly + recent_sales ใน 1 request |
| `GET` | `/api/report/daily` | ยอดขายรายวัน (default 7 วัน) |
| `GET` | `/api/report/monthly` | สรุปรายเดือน (default 12 เดือน) |
| `GET` | `/api/report/top-drugs` | ยาขายดี |
| `GET` | `/api/report/slow-drugs` | ยาขายช้า |
| `GET` | `/api/report/eod` | สรุปปิดยอดประจำวัน |
| `GET` | `/api/report/profit` | รายงานกำไร |

**Dashboard** — `GET /api/report/dashboard?days=7` ใช้ goroutine + `sync.WaitGroup` fetch parallel 4 dataset (summary, daily, monthly 12m, recent 5 sales) → ลด HTTP round-trip จาก 5-6 requests เหลือ 1 request สำหรับ ReportPage initial load

### KY Forms

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET/POST` | `/api/ky9` | แบบ ขย.9 |
| `GET/POST` | `/api/ky10` | แบบ ขย.10 |
| `GET/POST` | `/api/ky11` | แบบ ขย.11 |
| `GET/POST` | `/api/ky12` | แบบ ขย.12 |
| `GET` | `/api/export/:form` | Export PDF (ky9, ky10, ky11, ky12) |

### Settings (Singleton per tenant)

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/settings` | ดึงการตั้งค่าร้าน · สร้าง default ถ้ายังไม่มี |
| `PUT` | `/api/settings` | บันทึกการตั้งค่า (ADMIN/SUPER) |

รวม: ข้อมูลร้าน, ใบเสร็จ (header/footer/paper_width), Stock thresholds, เภสัชกร, ขย.toggle, และ **timezone** (IANA name) — ใช้คำนวณ "วันนี้"/"เดือนนี้" ในรายงานและ date-range filter ทั่วทั้งระบบ

### Movements

| Method | Path | คำอธิบาย |
|--------|------|-----------|
| `GET` | `/api/movements` | ประวัติการเคลื่อนไหวสต็อก (นำเข้า/ขาย/คืน/ปรับ/ตัดจำหน่าย/ยกเลิก) — กรองวันที่, ประเภท, ชื่อยา |

---

## Multi-Tenant Architecture

แต่ละ `clientId` (จาก JWT claim) มี MongoDB database แยกกัน:

| clientId | Database |
|----------|----------|
| `"000"` | `pharmacy` (default) |
| `"abc"` | `pharmacy_abc` |

`Manager.ForClient(clientId)` ทำ lazy init ด้วย `sync.Map` + `sync.Once`:
- **ครั้งแรก**: สร้าง `*MongoDB` → รัน `CreateIndexes` ใน background goroutine
- **ครั้งต่อไป**: คืน cached instance ทันที (lock-free)
- **clientId validation**: ต้องตรง `^[a-zA-Z0-9_-]+$` — มิฉะนั้น return `403 Forbidden`
- **Poison eviction**: ถ้า init ล้มเหลว จะลบ entry ออกจาก cache เพื่อให้ retry ได้

Bootstrap (main.go): `000` ถูก warm-up โดย `CreateIndexesForClient("000")` ตอนเริ่มต้น

---

## RBAC (Role-Based Access Control)

| Role | สิทธิ์ |
|------|--------|
| `SUPER` | ทุกอย่าง |
| `ADMIN` | จัดการยา, ขาย, รายงาน, นำเข้า, ซัพพลายเออร์, แบบฟอร์ม ขย. |
| `USER` | ขาย, ดูประวัติ, ดูสต็อก, จัดการลูกค้า (read + add) |

### Endpoints ที่ต้องการ ADMIN หรือ SUPER

- `POST /api/drugs`, `PUT /api/drugs/:id`, `POST /api/drugs/bulk`, `GET /api/drugs/reorder-suggestions`
- `POST /api/drugs/:id/adjustments`, `POST /api/drugs/:id/lots`, `DELETE /api/drugs/:id/lots/:lot_id`, `POST /api/lots/writeoff`
- `PUT /api/customers/:id`
- `POST /api/sales/:id/void`
- `GET /api/report/eod`, `GET /api/report/profit`
- `GET|POST /api/ky9`, `GET|POST /api/ky10`, `GET|POST /api/ky11`, `GET|POST /api/ky12`
- `/api/imports/*` ทั้งหมด
- `/api/suppliers/*` ทั้งหมด
- `GET /api/export/:form`

### Endpoints ที่ USER เข้าถึงได้

`GET /api/drugs`, `GET /api/drugs/low-stock`, `GET /api/drugs/:id/lots`, `GET /api/lots/expiring`,
`GET|POST /api/customers`, `GET /api/customers/:id/sales`,
`GET|POST /api/sales`, `GET /api/sales/:id/items`, `POST /api/sales/:id/return`, `GET /api/sales/:id/returns`,
`GET /api/report/summary`, `GET /api/report/dashboard`, `GET /api/report/daily`, `GET /api/report/monthly`, `GET /api/report/top-drugs`, `GET /api/report/slow-drugs`,
`GET /api/movements`

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
    Barcode     string         // บาร์โค้ดหน่วยหลัก (partial unique — ว่างได้; scanner fallback ใช้ RegNo)
    SellPrice   float64        // ราคาขายเริ่มต้น (bson:"price" backward-compat; = Prices.retail)
    CostPrice   float64        // ราคาทุน
    Stock       int            // คงเหลือรวมในหน่วยหลัก (= Σ DrugLot.Remaining)
    MinStock    int            // จุดสั่งซื้อขั้นต่ำ (0 = ใช้ Settings.stock.low_stock_threshold)
    RegNo       string         // เลขทะเบียนยา
    Unit        string         // หน่วยหลัก เช่น "เม็ด", "แคปซูล"
    ReportTypes []string       // ["ky9", "ky10", "ky11", "ky12"]
    AltUnits    []AltUnit      // หน่วยทางเลือก (แผง, กล่อง, …) — ดูด้านล่าง
    Prices      PriceTiers     // map[tier]price (retail/regular/wholesale/custom)
    CreatedAt   time.Time      // วันที่สร้าง
}
```

**การสร้างยา** — `POST /api/drugs` เข้มงวด: ถ้า `stock > 0` ต้องมี `create_lot` ไปด้วยเสมอ → backend ทำ transaction สร้าง Drug + DrugLot พร้อมกัน จึงการันตีว่า `drug.stock` มาพร้อม lot จริงเสมอ (FEFO ทำงานถูก) · `POST /api/drugs/bulk` ผ่อนคลายกฎนี้ — ยอม import ยาที่มี stock ค้างไว้ก่อน แล้วไปเพิ่ม lot ภายหลัง

### Multi-unit & Multi-tier Pricing

```go
type PriceTiers map[string]float64   // "retail" | "regular" | "wholesale" | custom

type AltUnit struct {
    Name      string      // "แผง", "กล่อง"
    Factor    int         // ≥ 2 — 1 alt = N base (1 แผง = 10 เม็ด)
    SellPrice float64     // back-compat mirror ของ Prices["retail"]
    Prices    PriceTiers  // ราคาต่อ alt unit (tier map)
    Barcode   string      // optional — สแกนแล้วเลือกหน่วยนี้อัตโนมัติ
    Hidden    bool        // true = ไม่โชว์ในตัวเลือกตอนขาย (ข้อมูลเก่าคงไว้)
}
```

- **Base unit first**: Stock, lots, reports ทั้งหมดเก็บและคำนวณในหน่วยหลักเสมอ · alt_units เป็น UI/pricing convenience ที่คูณ qty × factor ก่อนตัดสต็อก
- **Tier fallback**: `resolveTierPrice(tier)` → `tier → retail → SellPrice` เพื่อรองรับเอกสารรุ่นเก่า
- **Legacy compat**: schema เก่าที่เก็บ `Prices` เป็น struct `{retail, regular, wholesale}` decode เข้า map ได้โดยไม่ต้อง migrate
- **Scanner precedence**: `alt_unit.barcode` (non-hidden) → `drug.barcode` → `reg_no`
- **Customer-linked tier**: `Customer.PriceTier` ถ้าไม่ว่างจะ auto-apply ทั้งตะกร้าเมื่อเลือกลูกค้า

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
    ID            bson.ObjectID  // id
    SaleID        bson.ObjectID  // อ้างอิงบิล
    DrugID        bson.ObjectID  // อ้างอิงยา
    DrugName      string         // ชื่อยา
    Qty           int            // จำนวนในหน่วยที่ขาย (Unit)
    Price         float64        // ราคาต่อหน่วยหลังหักส่วนลดแล้ว (ในหน่วยที่ขาย)
    OriginalPrice float64        // ราคาขายเดิม (ก่อนลด)
    ItemDiscount  float64        // ส่วนลดต่อหน่วย
    Subtotal      float64        // รวม (Price × Qty)
    CostSubtotal  float64        // ต้นทุนรวม
    Unit          string         // "" = หน่วยหลัก · ชื่อ alt unit ถ้าขายเป็นแผง/กล่อง
    UnitFactor    int            // 1 = base, ≥2 = alt (ใช้คูณตัดสต็อก)
    PriceTier     string         // tier ที่ใช้ขาย (retail/regular/wholesale/custom)
}
```

`SaleResponse` ที่ส่งกลับจาก `POST /api/sales` มี `stock_updates: [{drug_id, new_stock}]` เพื่อให้ client อัปเดต state ของ drug ที่ขายไปโดยไม่ต้อง refetch ทั้ง list

### Customer

```go
type Customer struct {
    ID         bson.ObjectID  // id
    Name       string         // ชื่อ
    Phone      string         // เบอร์โทร (partial unique index — ว่างได้, ซ้ำไม่ได้)
    Disease    string         // โรคประจำตัว / แพ้ยา
    PriceTier  string         // "" = หน้าร้าน · "regular"/"wholesale"/custom — auto-apply ตอนเลือก
    TotalSpent float64        // ยอดซื้อสะสม
    LastVisit  *time.Time     // เข้าร้านล่าสุด
    CreatedAt  time.Time      // วันที่สร้าง
}
```

**Duplicate phone** — ตั้งแต่เพิ่ม partial unique index บน `phone` (เฉพาะ non-empty) → `POST`/`PUT /api/customers` จะคืน `409 Conflict` ถ้าเบอร์นี้มีอยู่ในระบบแล้ว

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
    StockValue float64  // มูลค่าสต็อก (= Σ cost_price × stock)
    LowStock   int      // ยาที่ stock ∈ (0, threshold]; threshold = min_stock หรือ 20
    OutStock   int      // ยาที่ stock = 0
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

type Dashboard struct {
    Summary     ReportSummary   // สรุปภาพรวม (ดูด้านบน)
    Daily       []DailyData     // ยอดขายรายวัน N วัน
    Monthly     []MonthlyData   // สรุป 12 เดือนล่าสุด
    RecentSales []Sale          // 5 บิลล่าสุด
}

type Settings struct {
    // Singleton (key="singleton") — one document per tenant. Auto-created with
    // defaults on first GET.
    Store      StoreInfo        // name, address, phone, tax_id
    Receipt    ReceiptSettings  // header, footer, paper_width ("58"|"80"), show_pharmacist
    Stock      StockSettings    // low_stock_threshold, reorder_days, reorder_lookahead, expiring_days
    Pharmacist PharmacistInfo   // name, license_no — ใช้ใน Receipt footer + ขย.11 auto-fill
    KY         KYSettings       // skip_auto, default_buyer_address
    Timezone   string           // IANA ("Asia/Bangkok") — reports + day filters
    UpdatedAt  time.Time
}
```

**Timezone usage** — handler ที่แปลง `YYYY-MM-DD` ↔ `time.Time` (report, sales, movements, imports) ทุกตัวเรียก `loadTimezone(ctx, mdb)` → ใช้ค่านี้ parse/format · fallback เป็น `Asia/Bangkok` เมื่อไม่ได้ตั้งหรือ IANA name ผิด

```go
type ReorderSuggestion struct {
    DrugID       string   // hex id
    DrugName     string
    Unit         string
    CurrentStock int
    MinStock     int
    QtySold      int      // ยอดขายรวมในช่วง lookback (ต้อง > 0 ถึงจะเข้า list)
    AvgDailySale float64  // = qty_sold / days
    DaysLeft     float64  // = current_stock / avg_daily
    SuggestedQty int      // = ceil(avg_daily × lookahead) − current_stock
    CostPrice    float64
    SellPrice    float64
}
```
