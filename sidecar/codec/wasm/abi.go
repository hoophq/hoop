package wasm

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// The export and import names of abi/ABI.md. Load compares them against
// the compiled module before anything runs: a wrong signature found at the
// first call would be a connection dropped in production for a mistake the
// author could have been told about at load.
const (
	expMemory        = "memory"
	expAlloc         = "alloc"
	expFree          = "free"
	expDescribe      = "describe"
	expDecode        = "decode"
	expDeny          = "deny"
	expFilter        = "filter"
	expEnableRewrite = "enable_rewrite"
	expRewrite       = "rewrite"
	expFlush         = "flush"
	expCredential    = "take_credential"
	expContent       = "content"
	expOpen          = "open"
	expClose         = "close"
	expInitialize    = "_initialize"

	hostModule     = "hoop"
	impAnalyzeSQL  = "analyze_sql"
	impSplitSQL    = "split_sql"
	impMask        = "mask"
	impLog         = "log"
	wasiModuleName = "wasi_snapshot_preview1"
)

// signature is one function type in the ABI's terms: u32 parameters and
// u32 or u64 results.
type signature struct {
	params  []api.ValueType
	results []api.ValueType
}

var (
	i32 = api.ValueTypeI32
	i64 = api.ValueTypeI64

	sigLen4Packed = signature{[]api.ValueType{i32, i32, i32, i32}, []api.ValueType{i64}}
	sigLen3Packed = signature{[]api.ValueType{i32, i32, i32}, []api.ValueType{i64}}

	// exportSignatures is every export the ABI names. requiredExports lists
	// the required ones; inspectModule checks the rest only when present.
	exportSignatures = map[string]signature{
		expAlloc:         {[]api.ValueType{i32}, []api.ValueType{i32}},
		expFree:          {[]api.ValueType{i32, i32}, nil},
		expDescribe:      {nil, []api.ValueType{i64}},
		expDecode:        sigLen4Packed,
		expDeny:          sigLen4Packed,
		expFilter:        sigLen4Packed,
		expEnableRewrite: {[]api.ValueType{i32}, nil},
		expRewrite:       sigLen3Packed,
		expFlush:         {[]api.ValueType{i32}, []api.ValueType{i64}},
		expCredential:    sigLen3Packed,
		expContent:       sigLen3Packed,
		expOpen:          {[]api.ValueType{i32, i32, i32}, []api.ValueType{i32}},
		expClose:         {[]api.ValueType{i32}, nil},
		expInitialize:    {nil, nil},
	}
	requiredExports = []string{expAlloc, expFree, expDescribe, expDecode}

	// capabilityExports maps a manifest capability to the exports that
	// announce it. checkCapabilities enforces both directions: a capability
	// needs all of its exports, and any of these exports needs its capability.
	capabilityExports = map[string][]string{
		CapDeny:       {expDeny},
		CapFilter:     {expFilter},
		CapRewrite:    {expEnableRewrite, expRewrite, expFlush},
		CapCredential: {expCredential},
		CapContent:    {expContent},
	}

	importSignatures = map[string]signature{
		impAnalyzeSQL: {[]api.ValueType{i32, i32}, []api.ValueType{i64}},
		impSplitSQL:   {[]api.ValueType{i32, i32}, []api.ValueType{i64}},
		impMask:       sigLen4Packed,
		impLog:        {[]api.ValueType{i32, i32, i32}, nil},
	}
)

// moduleShape is what the compiled module declares, read once at load.
type moduleShape struct {
	exports     map[string]bool // ABI function exports present
	importsWASI bool
	importsSQL  bool // analyze_sql or split_sql, which need a dialect
}

// inspectModule checks the compiled module's imports and exports against
// the ABI tables and reports which optional pieces it carries. It runs
// before describe, because instantiating a module whose imports the host
// cannot satisfy fails inside wazero with a message naming no ABI rule.
func inspectModule(c wazero.CompiledModule) (moduleShape, error) {
	shape := moduleShape{exports: map[string]bool{}}

	for _, def := range c.ImportedFunctions() {
		module, name, _ := def.Import()
		switch module {
		case hostModule:
			want, ok := importSignatures[name]
			if !ok {
				return shape, fmt.Errorf("imports hoop.%s, which the host does not provide", name)
			}
			if err := checkSignature(def, want); err != nil {
				return shape, fmt.Errorf("import hoop.%s: %w", name, err)
			}
			if name == impAnalyzeSQL || name == impSplitSQL {
				shape.importsSQL = true
			}
		case wasiModuleName:
			shape.importsWASI = true
		default:
			return shape, fmt.Errorf("imports %s.%s: a plug-in may import only %s and %s",
				module, name, hostModule, wasiModuleName)
		}
	}
	if n := len(c.ImportedMemories()); n > 0 {
		return shape, fmt.Errorf("imports a memory; the guest must own and export its own")
	}

	if _, ok := c.ExportedMemories()[expMemory]; !ok {
		return shape, fmt.Errorf("does not export %q", expMemory)
	}
	exported := c.ExportedFunctions()
	for name, def := range exported {
		want, ok := exportSignatures[name]
		if !ok {
			// Toolchains add their own (cabi_realloc, __main_void, ...);
			// inspectModule holds only the ABI's names to a signature.
			continue
		}
		if err := checkSignature(def, want); err != nil {
			return shape, fmt.Errorf("export %s: %w", name, err)
		}
		shape.exports[name] = true
	}
	for _, name := range requiredExports {
		if !shape.exports[name] {
			return shape, fmt.Errorf("does not export %q", name)
		}
	}
	return shape, nil
}

// checkCapabilities holds the manifest and the module to each other.
func checkCapabilities(m Manifest, shape moduleShape) error {
	for _, cap := range m.Capabilities {
		for _, exp := range capabilityExports[cap] {
			if !shape.exports[exp] {
				return fmt.Errorf("manifest names capability %q but the module does not export %q", cap, exp)
			}
		}
	}
	for cap, exps := range capabilityExports {
		if m.hasCapability(cap) {
			continue
		}
		for _, exp := range exps {
			if shape.exports[exp] {
				return fmt.Errorf("module exports %q but the manifest does not name capability %q", exp, cap)
			}
		}
	}
	if shape.importsSQL && m.SQLDialect == "" {
		return fmt.Errorf("module imports hoop.%s or hoop.%s but the manifest names no sql_dialect", impAnalyzeSQL, impSplitSQL)
	}
	if shape.importsWASI != m.WASI {
		if m.WASI {
			return fmt.Errorf("manifest says wasi but the module imports nothing from %s", wasiModuleName)
		}
		return fmt.Errorf("module imports %s but the manifest does not say wasi", wasiModuleName)
	}
	return nil
}

func checkSignature(def api.FunctionDefinition, want signature) error {
	if !slices.Equal(def.ParamTypes(), want.params) || !slices.Equal(def.ResultTypes(), want.results) {
		return fmt.Errorf("signature is %s, the ABI wants %s",
			describeSignature(def.ParamTypes(), def.ResultTypes()),
			describeSignature(want.params, want.results))
	}
	return nil
}

func describeSignature(params, results []api.ValueType) string {
	names := func(ts []api.ValueType) string {
		out := make([]string, len(ts))
		for i, t := range ts {
			out[i] = api.ValueTypeName(t)
		}
		return strings.Join(out, ", ")
	}
	return "(" + names(params) + ") -> (" + names(results) + ")"
}
