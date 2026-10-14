package compliance

import (
	"bytes"
	"context"
	"fmt"

	"pharmacy-pos/backend/db"
	"pharmacy-pos/backend/models"
	"pharmacy-pos/backend/pdf"
)

// ShopFrom is the shop a register is printed for, from the settings the
// shop runs on (its name, ขย. licence and address).
func ShopFrom(s models.Settings) pdf.Shop {
	return pdf.Shop{Name: s.Store.Name, LicenseNo: s.Store.LicenseNo, Address: s.Store.Address}
}

// Print is a month of a register as its legal PDF: the rows oldest first,
// headed with the shop that keeps the register.
func Print(ctx context.Context, mdb *db.MongoDB, form Form, month string) (*bytes.Buffer, error) {
	shop := ShopFrom(mdb.StoreSettings(ctx))
	switch form {
	case Ky9:
		return printMonth(ctx, mdb, month, shop, pdf.GenerateKy9)
	case Ky10:
		return printMonth(ctx, mdb, month, shop, pdf.GenerateKy10)
	case Ky11:
		return printMonth(ctx, mdb, month, shop, pdf.GenerateKy11)
	case Ky12:
		return printMonth(ctx, mdb, month, shop, pdf.GenerateKy12)
	}
	return nil, fmt.Errorf("compliance: no printed register for %q", form)
}

func printMonth[T Row](ctx context.Context, mdb *db.MongoDB, month string, shop pdf.Shop, render func([]T, string, pdf.Shop) (*bytes.Buffer, error)) (*bytes.Buffer, error) {
	rows, err := Month[T](ctx, mdb, month, OldestFirst)
	if err != nil {
		return nil, err
	}
	return render(rows, month, shop)
}
