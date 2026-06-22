// JSX module: a FUNCTION component with a function-typed prop, a CLASS component
// extending a member-expression base (React.Component), a hook (external dep),
// and a MIXIN pattern (`extends Mixin(Base)`).
//
// NOTE: aracne does not model JSX element usage as edges, and React components
// are indistinguishable from ordinary functions/classes. This file mainly
// confirms JSX/TSX parses without errors and that components are extracted.
import React, { useState } from "react";
import { Circle } from "./shapes.js";

// React component that renders a button displaying a shape's description and click count, calling onSelect when clicked
export function ShapeBadge({ shape, onSelect }) {
  const [count, setCount] = useState(0);
  const handle = () => {
    setCount(count + 1);
    onSelect(shape);
  };
  return <button onClick={handle}>{shape.describe()} {count}</button>;
}

// React component that renders a circle's area based on the radius prop.
export class CircleView extends React.Component {
// Instantiates a Circle with the provided radius and renders its computed area in a div.
  render() {
    const c = new Circle(this.props.radius); // `new` -> method resolves
    return <div>{c.area()}</div>;
  }
}

// Higher-order function that wraps a class with a serialize method to output JSON.
const Serializable = (Base) =>
  class SerializableMixin extends Base {
    serialize() {
      return JSON.stringify(this);
    }
  };

// Circle subclass mixed with Serializable, computing area with fallback to zero if parent method unavailable.
export class SerializableCircle extends Serializable(Circle) {
// Computes the circle's area by calling the parent method or returns zero if unavailable.
  area() {
    return super.area ? super.area() : 0;
  }
}
