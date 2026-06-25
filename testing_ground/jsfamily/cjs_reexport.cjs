// EDGE: whole-module re-export via `module.exports = require('./shapes.js')`.
// SHOULD: this module re-exports everything from shapes.js (consumers see
// Circle/Rectangle through it). ACTUAL (observe live): parseCommonJSExport only
// handles module.exports = <identifier | object literal>; a module.exports
// bound to a require(...) call expression is expected to be dropped.
const shapes = require("./shapes.js");

// Indirect whole-module re-export.
module.exports = shapes;
