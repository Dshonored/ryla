package views

import "github.com/Dshonored/ryla/validate"

// AriaInvalid marks a field for screen readers, so a validation error is not
// something only sighted users find out about.
func AriaInvalid(bag validate.ErrorBag, field string) string {
	if bag.Has(field) {
		return "true"
	}
	return "false"
}
