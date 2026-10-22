package parser

// recursionLimit is how deep expressions may nest, as upstream's
// RECURSION_LIMIT, which is smaller on Windows.
const recursionLimit = 48
