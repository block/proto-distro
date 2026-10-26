package fileutil

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

type PlanInput struct{ Path, SHA256, Use string }
type PlanOutput struct{ Path, Action string }

// WritePlan reports selection, not cryptographic validity or future execution success.
func WritePlan(w io.Writer, inputs []PlanInput, outputs []PlanOutput) error {
	if w == nil {
		w = os.Stdout
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Path < outputs[j].Path })
	for _, input := range inputs {
		if _, err := fmt.Fprintf(w, "input %s %s %q\n", input.Use, input.SHA256, input.Path); err != nil {
			return err
		}
	}
	for _, output := range outputs {
		if _, err := fmt.Fprintf(w, "output %s %q\n", output.Action, output.Path); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, "Dry run: no files changed.")
	return err
}

// FileInput hashes a physical regular file, never its contents in a report.
func FileInput(name, use string) (PlanInput, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return PlanInput{}, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return PlanInput{}, err
	}
	if !info.Mode().IsRegular() {
		return PlanInput{}, fmt.Errorf("not a regular input file: %s", absolute)
	}
	hash, err := SHA256File(absolute)
	return PlanInput{Path: absolute, SHA256: hash, Use: use}, err
}
