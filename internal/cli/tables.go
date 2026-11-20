package cli

import (
	"encoding/csv"
	"fmt"
	"strings"
)

// table and v0Table are read from the spec CSV files that table_gen.go
// holds (optionsCSV, v0CSV).
var (
	table   []Option
	v0Table []V0Flag
)

// mustReadTables reads the two CSV files. A broken file is a build error
// in effect: the tests and every start fail at once.
func mustReadTables() ([]Option, []V0Flag) {
	opts, err := readCSV(optionsCSV, 11)
	if err != nil {
		panic(fmt.Sprintf("cli: v1-options.csv: %v", err))
	}
	v0, err := readCSV(v0CSV, 3)
	if err != nil {
		panic(fmt.Sprintf("cli: v0-to-v1.csv: %v", err))
	}
	options := make([]Option, len(opts))
	for i, r := range opts {
		options[i] = Option{Name: r[0], Group: r[2], Scope: r[3], Type: r[4], Default: r[5],
			Curl: r[6], Wget: r[7], Aria2: r[8], Phase: r[9], Help: r[10]}
		if len(r[1]) == 2 {
			options[i].Short = r[1][1]
		}
	}
	flagsV0 := make([]V0Flag, len(v0))
	for i, r := range v0 {
		flagsV0[i] = V0Flag{V0: r[0], V1: r[1], Note: r[2]}
	}
	return options, flagsV0
}

// readCSV returns the rows after the header, each with n fields.
func readCSV(text string, n int) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = n
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	return rows[1:], nil
}
