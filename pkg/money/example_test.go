// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package money_test

import (
	"fmt"

	"abarow.com/pkg/money"
)

func Example() {
	price := money.Money(8990) // 89,90 TL/kg
	qty := money.Qty(1250)     // 1,250 kg
	line := price.MulQty(qty)

	fmt.Println("Tutar:", line)
	fmt.Println("KDV (%1):", line.IncludedTax(100))
	// Output:
	// Tutar: 112,38
	// KDV (%1): 1,11
}
