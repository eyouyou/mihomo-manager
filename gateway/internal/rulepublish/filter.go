package rulepublish

import (
	"errors"
	"os"
	"slices"
)

// CheckStartupFilter rejects edits that the deployment's startup processing
// would undo or reorder, including changes to rules owned by that processing.
func CheckStartupFilter(before, after string) error {
	a, err := readRows(before)
	if err != nil {
		return err
	}
	b, err := readRows(after)
	if err != nil {
		return err
	}
	if !slices.Equal(a, b) {
		return errors.New("启动预处理会改变候选规则")
	}
	return nil
}

type startupRow struct {
	value    string
	disabled bool
}

func readRows(path string) ([]startupRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := readConfig(raw)
	if err != nil {
		return nil, err
	}
	rows := make([]startupRow, 0, len(doc.rows))
	for i, node := range doc.rows {
		rows = append(rows, startupRow{node.Value, doc.disabled[i]})
	}
	return rows, nil
}
