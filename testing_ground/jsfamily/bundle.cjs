// CommonJS module exercising the `module.exports = { ... }` object form and a
// whole-module require with a member call.
const helper = require("./commonjs.cjs");

// Returns sum of circle area (radius 2) and rectangle area (2x3)
function summary() {
  return helper.buildCircle(2) + helper.rectInfo(2, 3);
}

module.exports = { summary };
