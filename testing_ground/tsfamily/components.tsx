// TSX module (parsed with the dedicated tsx grammar): a typed FUNCTION component
// whose props come from an interface, a function-typed prop, a CLASS component
// extending a GENERIC member-expression base (React.Component<Props>), enum
// usage, and a local annotation that drives method resolution.
import React from "react";
import { Circle } from "./shapes";
import { Color } from "./models";

// Props interface for badge component with shape, color, and selection callback.
interface BadgeProps {
  shape: Circle;
  color: Color;
  onSelect: (shape: Circle) => void;
}

// React component that renders a button displaying a shape's area and handles shape selection.
export function ShapeBadge(props: BadgeProps) {
  const handle = () => props.onSelect(props.shape);
  return <button onClick={handle}>{props.shape.area()}</button>;
}

// React component that renders a circle shape with color styling and area display.
export class CircleView extends React.Component<BadgeProps> {
// Renders the circle with computed area and color style from props.
  render() {
    const c: Circle = this.props.shape; // local annotation -> `c.area()` resolves
    return <div style={{ color: Color[this.props.color] }}>{c.area()}</div>;
  }
}
