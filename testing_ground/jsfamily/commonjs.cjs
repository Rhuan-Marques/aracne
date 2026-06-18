// CommonJS module: a DESTRUCTURED require of an internal module, an EXTERNAL
// require, `new`-based method resolution, and `exports.x = ...` named exports.
const { Circle, Rectangle } = require("./shapes.js");
const path = require("path"); // external dependency

function buildCircle(radius) {
  const c = new Circle(radius);
  return c.area();
}

function rectInfo(w, h) {
  const r = new Rectangle(w, h);
  return r.area();
}

exports.buildCircle = buildCircle;
exports.rectInfo = rectInfo;
exports.platformSep = path.sep; // uses the external dependency
