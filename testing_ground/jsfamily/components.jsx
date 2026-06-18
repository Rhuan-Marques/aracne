// JSX module: a FUNCTION component with a function-typed prop, a CLASS component
// extending a member-expression base (React.Component), a hook (external dep),
// and a MIXIN pattern (`extends Mixin(Base)`).
//
// NOTE: aracne does not model JSX element usage as edges, and React components
// are indistinguishable from ordinary functions/classes. This file mainly
// confirms JSX/TSX parses without errors and that components are extracted.
import React, { useState } from "react";
import { Circle } from "./shapes.js";

// Function component. `onSelect` is a function-typed prop that gets called.
export function ShapeBadge({ shape, onSelect }) {
  const [count, setCount] = useState(0);
  const handle = () => {
    setCount(count + 1);
    onSelect(shape);
  };
  return <button onClick={handle}>{shape.describe()} {count}</button>;
}

// Class component extending a member-expression base (React.Component).
export class CircleView extends React.Component {
  render() {
    const c = new Circle(this.props.radius); // `new` -> method resolves
    return <div>{c.area()}</div>;
  }
}

// A mixin: a function returning a (named) class expression.
const Serializable = (Base) =>
  class SerializableMixin extends Base {
    serialize() {
      return JSON.stringify(this);
    }
  };

// `extends Serializable(Circle)` is the `extends Mixin(Base)` edge case: the
// base resolves by the call's callee name ("Serializable").
export class SerializableCircle extends Serializable(Circle) {
  area() {
    return super.area ? super.area() : 0;
  }
}
