package app

import (
	"fmt"
	"reflect"
)

// complete checks that every interface field of the given structs (the
// Repositories of each package) is set, so a repository forgotten in the
// wiring stops startup instead of failing at its first use.
func complete(structs ...any) error {
	for _, s := range structs {
		v := reflect.ValueOf(s)
		for i := range v.NumField() {
			if f := v.Field(i); f.Kind() == reflect.Interface && f.IsNil() {
				return fmt.Errorf("wiring: %s.%s is not set", v.Type(), v.Type().Field(i).Name)
			}
		}
	}
	return nil
}

// checked records in errs whether a Repositories struct is complete and
// returns it.
func checked[T any](errs *[]error, v T) T {
	if err := complete(v); err != nil {
		*errs = append(*errs, err)
	}
	return v
}
