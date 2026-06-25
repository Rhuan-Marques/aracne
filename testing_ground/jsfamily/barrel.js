// EDGE: barrel / re-export module. Aracne's parseExport ignores the `from`
// source on an export clause and has no handling for `export *` /
// `export * as ns`, so re-exported bindings are expected to be LOST here.
//
// SHOULD: a consumer importing { Disc } from this barrel resolves transitively
// to shapes.Circle; importing makeSquare resolves to shapes.makeSquare (the
// default export). ACTUAL (observe live): the re-export source is dropped, so
// the barrel re-exports nothing usable and downstream calls do not reach shapes.*.

export { Circle, Rectangle } from "./shapes.js"; // named re-export
export { Circle as Disc } from "./shapes.js"; // aliased re-export
export { default } from "./shapes.js"; // default re-export (makeSquare)
export { default as makeSquare } from "./shapes.js"; // default-as-named re-export
export * from "./factory.js"; // star re-export
export * as factoryNS from "./factory.js"; // namespace-star re-export
