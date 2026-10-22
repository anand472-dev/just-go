//go:build !windows

package parser

// recursionLimit is how deep expressions may nest, as upstream's
// RECURSION_LIMIT.
const recursionLimit = 256
