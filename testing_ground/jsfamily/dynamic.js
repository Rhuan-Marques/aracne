// EDGE: dynamic import() expressions and import.meta. SHOULD: a dynamic import
// registers a module dependency on shapes.js / factory.js and the member access
// on the imported namespace resolves. ACTUAL (observe live): only static import
// and require are parsed, so dynamic import() is expected to produce NO module
// dependency edge and NO resolution of mod.Circle / m.makeCircle.
export async function lazyCircle(r) {
  const mod = await import("./shapes.js"); // dynamic import
  return new mod.Circle(r).area(); // member-of-dynamic-namespace `new`
}

// Dynamic import via .then().
export function lazyThen() {
  return import("./factory.js").then((m) => m.makeCircle(1));
}

// import.meta usage (must not crash the parser).
export const baseUrl = import.meta.url;
