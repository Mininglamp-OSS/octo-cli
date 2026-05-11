// Package output is a leaf package that owns the CLI's user-facing I/O
// contract: the JSON success envelope, the error envelope, and the error
// taxonomy + exit codes. It also handles secondary formats (table, csv,
// ndjson) and the --jq filter pass.
//
// Leaf invariant: this package must not import any other internal/*
// package. Everything flows in: ExitError is the structured error type
// every CLI path converges on; WriteSuccess / WriteError are the only
// sanctioned way to emit a response. Keeping this a leaf avoids cyclic
// dependencies and keeps the envelope contract as a single, auditable
// boundary between the CLI layers and what agents parse.
package output
