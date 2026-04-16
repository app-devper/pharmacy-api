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
│   └── export.go
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
    SellPrice   float64        // ราคาขาย
    CostPrice   float64        // ราคาทุน
    Stock       int            // จำนวนคงเหลือรวม
    Unit        string         // หน่วย เช่น "เม็ด", "แคปซูล"
    ReportTypes []string       // ["ky9", "ky10", "ky11", "ky12"]
}
```

### Drug Lot (FEFO)

ระบบใช้ **FEFO (First Expired, First Out)** — ตัดสต็อกจากล็อตที่ใกล้หมดอายุก่อนเสมอ

```go
type DrugLot struct {
    DrugID     bson.ObjectID  // อ้างอิงยา
    LotNumber  string         // หมายเลขล็อต
    ExpiryDate time.Time      // วันหมดอายุ (เรียงตามนี้)
    ImportDate time.Time      // วันที่นำเข้า
    CostPrice  *float64       // nil = ใช้ drug.CostPrice
    SellPrice  *float64       // nil = ใช้ drug.SellPrice
    Quantity   int            // จำนวนที่นำเข้า (immutable)
    Remaining  int            // คงเหลือในล็อตนี้ (ลดเมื่อขาย)
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
