package swurepackage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"proto-distro/tools/distro/internal/fileutil"
)

// Components names the replaceable payloads present in a seed archive.
type Components struct {
	Rootfs string
	FIP    string
}

func discoverComponents(names []string) (Components, error) {
	var result Components
	for _, name := range names {
		switch {
		case strings.HasSuffix(name, ".ext4.gz"):
			if result.Rootfs != "" {
				return Components{}, fmt.Errorf("seed requires one rootfs, one FIP, or both")
			}
			result.Rootfs = name
		case reservedFIP(name):
			if result.FIP != "" {
				return Components{}, fmt.Errorf("seed requires one rootfs, one FIP, or both")
			}
			result.FIP = name
		}
	}
	if result == (Components{}) {
		return result, fmt.Errorf("seed requires one rootfs, one FIP, or both")
	}
	return result, nil
}

// Inspect validates archive safety and discovers components without replacing them.
func Inspect(archive string) (Components, error) {
	names, err := listCPIO(archive)
	if err != nil {
		return Components{}, err
	}
	return discoverComponents(names)
}

// prepareRecreation checks component membership without extracting or writing.
func prepareRecreation(opts Options) (Options, []string, Components, error) {
	names, err := listCPIO(opts.OldSWU)
	if err != nil {
		return opts, nil, Components{}, err
	}
	components, err := discoverComponents(names)
	if err != nil {
		return opts, nil, components, err
	}
	if (components.Rootfs != "") != (opts.NewRootfs != "") {
		return opts, nil, components, fmt.Errorf("rootfs replacement must match seed rootfs presence")
	}
	if (components.FIP != "") != (opts.NewFIP != "") {
		return opts, nil, components, fmt.Errorf("FIP replacement must match seed FIP presence")
	}
	if components.Rootfs == "" && (opts.AVBBody != "" || opts.AVBTail != "") {
		return opts, nil, components, fmt.Errorf("AVB replacement requires seed rootfs")
	}
	if components.Rootfs != "" {
		opts, err = prepare(opts)
	} else {
		if opts.PrivateKey == "" && opts.KeyDir != "" {
			opts.PrivateKey = filepath.Join(opts.KeyDir, "priv.pem")
		}
		if opts.Passfile == "" && opts.KeyDir != "" {
			opts.Passfile = filepath.Join(opts.KeyDir, "passout")
		}
		if opts.Output == "" {
			opts.Output = strings.TrimSuffix(opts.OldSWU, ".swu") + "_repackaged.swu"
		}
		for _, path := range []string{opts.NewFIP, opts.PrivateKey, opts.Passfile} {
			if _, e := regularFile(path); e != nil {
				err = e
				break
			}
		}
	}
	return opts, names, components, err
}

type preparedRootfs struct {
	directory string
	payloads  []imagePayload
	plainHash string
}

// RebuildAll writes and verifies every archive before callers publish any output.
// Outputs must be explicit, distinct, absent paths in caller-owned staging.
func RebuildAll(options []Options) (err error) {
	if len(options) == 0 {
		return nil
	}
	type recreation struct {
		options    Options
		names      []string
		components Components
	}
	jobs := make([]recreation, len(options))
	destinations := make(map[string]bool, len(options))
	for i, opts := range options {
		if opts.Output == "" {
			return fmt.Errorf("batch rebuild requires explicit output paths")
		}
		destination, e := fileutil.Destination(opts.Output)
		if e != nil {
			return e
		}
		if destinations[destination] {
			return fmt.Errorf("duplicate batch destination: %s", destination)
		}
		destinations[destination] = true
		opts.Output = destination
		jobs[i].options, jobs[i].names, jobs[i].components, err = prepareRecreation(opts)
		if err != nil {
			return err
		}
	}
	scratch, err := os.MkdirTemp("", "swu-rebuild-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	var outputs []string
	defer func() {
		if err != nil {
			for _, output := range outputs {
				os.Remove(output)
			}
		}
	}()
	roots := make(map[[3]string]*preparedRootfs)
	for i, job := range jobs {
		var prepared *preparedRootfs
		if job.components.Rootfs != "" {
			key := [3]string{job.options.NewRootfs, job.options.AVBBody, job.options.AVBTail}
			prepared = roots[key]
			if prepared == nil {
				directory := filepath.Join(scratch, fmt.Sprintf("rootfs-%d", i))
				if err = os.Mkdir(directory, 0700); err != nil {
					return err
				}
				prepared = &preparedRootfs{directory: directory}
				prepared.payloads, err = stageRootfs(job.options, directory, "rootfs.ext4.gz")
				if err != nil {
					return err
				}
				prepared.plainHash, err = fileutil.SHA256File(job.options.NewRootfs)
				if err != nil {
					return err
				}
				roots[key] = prepared
			}
		}
		directory := filepath.Join(scratch, fmt.Sprintf("archive-%d", i))
		if err = os.Mkdir(directory, 0700); err != nil {
			return err
		}
		if err = recreateArchive(job.options, job.names, job.components, directory, prepared, true); err != nil {
			return fmt.Errorf("rebuilding %s: %w", job.options.OldSWU, err)
		}
		outputs = append(outputs, job.options.Output)
	}
	return nil
}

// Run preserves seed component membership, scripts, and payload names.
func recreate(opts Options) error {
	opts, names, components, err := prepareRecreation(opts)
	if err != nil {
		return err
	}
	if _, err = fileutil.Destination(opts.Output); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "swu-recreate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	outputDir, err := os.MkdirTemp("", "swu-publish-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outputDir)
	destination := opts.Output
	opts.Output = filepath.Join(outputDir, "output.swu")
	if err = recreateArchive(opts, names, components, directory, nil, false); err != nil {
		return err
	}
	return copyPayload(opts.Output, destination)
}

func recreateArchive(opts Options, names []string, components Components, directory string, prepared *preparedRootfs, strict bool) error {
	root := components.Rootfs
	if _, err := extractCPIO(opts.OldSWU, directory); err != nil {
		return err
	}
	path := filepath.Join(directory, "sw-description")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	source := string(data)
	replacements := map[string]string{}
	var expected map[string]string
	if strict && root != "" {
		expected = make(map[string]string, 3)
	}
	if root != "" {
		if strict {
			_, owned, e := rewriteDescription(source, root, []imagePayload{{name: root, compressed: true}})
			if e != nil {
				return e
			}
			for _, suffix := range []string{".avb.body", ".avb.tail"} {
				name := strings.TrimSuffix(root, ".gz") + suffix
				_, e := regularFile(filepath.Join(directory, name))
				if owned && e != nil {
					return fmt.Errorf("owned AVB member %s: %w", name, e)
				}
				if !owned && !os.IsNotExist(e) {
					return fmt.Errorf("unexpected owned AVB member %s", name)
				}
			}
		}
		if err = os.Remove(filepath.Join(directory, root)); err != nil {
			return err
		}
		for _, suffix := range []string{".avb.body", ".avb.tail"} {
			p := filepath.Join(directory, strings.TrimSuffix(root, ".gz")+suffix)
			if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		var payloads []imagePayload
		if prepared == nil {
			payloads, err = stageRootfs(opts, directory, root)
		} else {
			payloads = make([]imagePayload, len(prepared.payloads))
			for i, payload := range prepared.payloads {
				payloads[i] = payload
				payloads[i].name = root
				if i > 0 {
					payloads[i].name = strings.TrimSuffix(root, ".gz") + []string{".avb.body", ".avb.tail"}[i-1]
				}
				if err = copyPayload(filepath.Join(prepared.directory, payload.name), filepath.Join(directory, payloads[i].name)); err != nil {
					return err
				}
				if i > 0 {
					expected[payloads[i].name] = payload.hash
				}
			}
			expected[strings.TrimSuffix(root, ".gz")] = prepared.plainHash
		}
		if err != nil {
			return err
		}
		source, _, err = rewriteDescription(source, root, payloads)
		if err != nil {
			return err
		}
		for _, payload := range payloads {
			replacements[payload.name] = payload.hash
		}
	}
	fipHash := ""
	if components.FIP != "" {
		target := filepath.Join(directory, components.FIP)
		if err = os.Remove(target); err != nil {
			return err
		}
		if err = copyPayload(opts.NewFIP, target); err != nil {
			return err
		}
		hash, e := fileutil.SHA256File(target)
		if e != nil {
			return e
		}
		replacements[components.FIP] = hash
		fipHash = hash
	}
	// Change only replacement hashes. All scripts and selection policy stay trusted seed bytes.
	tokens, err := configTokens(source)
	if err != nil {
		return err
	}
	var edits []spanEdit
	counts := map[string]int{}
	for i, token := range tokens {
		if token.text != "{" {
			continue
		}
		record, e := recordFields(tokens, i)
		if e != nil {
			return e
		}
		name, _ := fieldString(tokens, record, "filename")
		hash, ok := replacements[name]
		if !ok {
			continue
		}
		field, ok := record.fields["sha256"]
		if !ok {
			return fmt.Errorf("replacement record missing sha256: %s", name)
		}
		edits = append(edits, spanEdit{tokens[field.value].start, tokens[field.valueEnd-1].end, fmt.Sprintf("%q", hash)})
		counts[name]++
	}
	for name := range replacements {
		if counts[name] == 0 {
			return fmt.Errorf("seed manifest does not reference replacement %s", name)
		}
	}
	source = applySpans(source, edits)
	if err = os.WriteFile(path, []byte(source), 0644); err != nil {
		return err
	}
	if err = signFile(path, filepath.Join(directory, "sw-description.sig"), opts.PrivateKey, opts.Passfile); err != nil {
		return fmt.Errorf("signing sw-description: %w", err)
	}
	var order []string
	for _, name := range names {
		if name == "sw-description" || name == "sw-description.sig" {
			continue
		}
		if root != "" && (name == strings.TrimSuffix(root, ".gz")+".avb.body" || name == strings.TrimSuffix(root, ".gz")+".avb.tail") {
			continue
		}
		order = append(order, name)
		if root != "" && name == root {
			order = append(order, strings.TrimSuffix(root, ".gz")+".avb.body", strings.TrimSuffix(root, ".gz")+".avb.tail")
		}
	}
	created, err := createCPIO(directory, opts.Output, order)
	if created {
		defer func() {
			if err != nil {
				os.Remove(opts.Output)
			}
		}()
	}
	if err != nil {
		return err
	}
	if strict {
		err = verify(opts.Output, components, expected, fipHash)
		return err
	}
	return nil
}

// Preview remains local and read-only.
func Preview(opts Options) ([]fileutil.PlanInput, []fileutil.PlanOutput, error) {
	opts, names, components, err := prepareRecreation(opts)
	if err != nil {
		return nil, nil, err
	}
	var inputs []fileutil.PlanInput
	for _, name := range []string{opts.OldSWU, opts.NewRootfs, opts.AVBBody, opts.AVBTail, opts.NewFIP, opts.PrivateKey, opts.Passfile} {
		if name == "" {
			continue
		}
		input, e := fileutil.FileInput(name, "used")
		if e != nil {
			return nil, nil, e
		}
		inputs = append(inputs, input)
	}
	outputs := []fileutil.PlanOutput{{Path: opts.Output, Action: "create"}}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
		outputs = append(outputs, fileutil.PlanOutput{Path: opts.Output + "!/" + name, Action: "create"})
	}
	if root := components.Rootfs; root != "" {
		for _, suffix := range []string{".avb.body", ".avb.tail"} {
			name := strings.TrimSuffix(root, ".gz") + suffix
			if !seen[name] {
				outputs = append(outputs, fileutil.PlanOutput{Path: opts.Output + "!/" + name, Action: "create"})
			}
		}
	}
	return inputs, outputs, nil
}
