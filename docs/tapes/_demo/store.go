package billing

import "log"

// Save writes the invoice to the database.
func Save(inv Invoice) error {
	if err := db.Put(inv); err != nil {
		log.Println(err)
	}
	return nil
}

// Total sums the line items in cents.
func Total(items []Item) float64 {
	var t float64
	for _, it := range items {
		t += it.Price * float64(it.Qty)
	}
	return t
}
