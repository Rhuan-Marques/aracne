// TSX module (parsed with the dedicated tsx grammar): a typed FUNCTION component
// whose props come from an interface, a function-typed prop, a CLASS component
// extending a GENERIC member-expression base (React.Component<Props>), enum
// usage, and a local annotation that drives method resolution.
import React from "react";
import { Circle } from "./shapes";
import { Color } from "./models";

// Interface used as a component's props, including a FUNCTION-TYPED prop.
interface BadgeProps {
  shape: Circle;
  color: Color;
  onSelect: (shape: Circle) => void;
}

// Function component with typed props.
export function ShapeBadge(props: BadgeProps) {
  const handle = () => props.onSelect(props.shape);
  return <button onClick={handle}>{props.shape.area()}</button>;
}

// Class component extending a generic member-expression base.
export class CircleView extends React.Component<BadgeProps> {
  render() {
    const c: Circle = this.props.shape; // local annotation -> `c.area()` resolves
    return <div style={{ color: Color[this.props.color] }}>{c.area()}</div>;
  }
}
