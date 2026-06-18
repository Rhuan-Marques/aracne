// CommonJS module exercising the `module.exports = { ... }` object form and a
// whole-module require with a member call.
const helper = require("./commonjs.cjs");

function summary() {
  return helper.buildCircle(2) + helper.rectInfo(2, 3);
}

module.exports = { summary };
