// EDGE: CommonJS `exports.x = ...` with an arrow function, a function
// expression, and a class expression on the right-hand side. SHOULD: scaled,
// build, and Wrapper are resources (functions / class) that use shapes.Circle.
// ACTUAL (observe live): localExportName only records the export name when the
// RHS is a bare identifier; arrow / function / class expressions assigned to
// exports.x are expected NOT to become their own function / class resources.
const { Circle } = require("./shapes.js");

// exports.x = arrow function.
exports.scaled = (r, k) => new Circle(r * k).area();

// exports.x = function expression.
exports.build = function build(r) {
  return new Circle(r).area();
};

// exports.x = class expression.
exports.Wrapper = class Wrapper {
  constructor(r) {
    this.c = new Circle(r);
  }
  area() {
    return this.c.area();
  }
};
