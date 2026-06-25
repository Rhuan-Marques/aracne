// EDGE: const enum, string enum, heterogeneous enum, enum-as-type, member access.
export const enum Dir {
  N,
  S,
  E,
  W,
}

export enum Status {
  Active = "ACTIVE",
  Closed = "CLOSED",
}

export enum Mixed {
  A,
  B = "b",
  C = 3,
}

// Enum used as a parameter / return type.
export function move(d: Dir): Dir {
  return d;
}

// Enum member access.
export function isActive(s: Status): boolean {
  return s === Status.Active;
}
