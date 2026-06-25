// EDGE: TypeScript import-equals (`import x = require(...)`) + member call.
// SHOULD: viaEquals resolves `new shapes.Circle` and Circle.area. ACTUAL
// (observe live): the import-equals binding may not be tracked like an ES/CJS
// import, leaving the namespaced member call unresolved.
import shapes = require("./shapes");

export function viaEquals(): number {
  return new shapes.Circle(1).area();
}
