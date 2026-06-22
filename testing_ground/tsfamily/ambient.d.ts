// Initializes the ambient environment with the given configuration.

declare function setup(config: SetupConfig): void;

// Configuration interface for setup with debug flag and log level.
declare interface SetupConfig {
  debug: boolean;
  level: number;
}

declare const VERSION: string;

declare namespace Native {
// Returns the current timestamp as a number.
  function now(): number;
}

export {};
