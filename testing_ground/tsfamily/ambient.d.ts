// Ambient declaration file (.d.ts): the TS scanner includes .d.ts files and
// parses `declare` statements. Exercises declared function/interface/const and
// an ambient namespace.

declare function setup(config: SetupConfig): void;

declare interface SetupConfig {
  debug: boolean;
  level: number;
}

declare const VERSION: string;

declare namespace Native {
  function now(): number;
}

export {};
