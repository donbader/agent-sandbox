/**
 * Lightweight structured logger for the mcp-oauth command plugin.
 * Outputs JSON to stderr, matching channel-manager's log format.
 * No external dependencies — keeps the plugin self-contained for testing.
 */

type LogData = Record<string, unknown>;

const LOG_LEVELS = ["debug", "info", "warn", "error"] as const;
type LogLevel = (typeof LOG_LEVELS)[number];

const configuredLevel: LogLevel = LOG_LEVELS.includes(process.env.LOG_LEVEL as LogLevel)
  ? (process.env.LOG_LEVEL as LogLevel)
  : "info";

const levelIndex = LOG_LEVELS.indexOf(configuredLevel);

interface Logger {
  debug(data: LogData, msg: string): void;
  info(data: LogData, msg: string): void;
  warn(data: LogData, msg: string): void;
  error(data: LogData, msg: string): void;
}

function formatLine(level: string, component: string, data: LogData, msg: string): string {
  return JSON.stringify({ level, time: Date.now(), component, ...data, msg });
}

export function createLogger(component: string): Logger {
  return {
    debug(data: LogData, msg: string) {
      if (levelIndex <= 0) process.stderr.write(formatLine("debug", component, data, msg) + "\n");
    },
    info(data: LogData, msg: string) {
      if (levelIndex <= 1) process.stderr.write(formatLine("info", component, data, msg) + "\n");
    },
    warn(data: LogData, msg: string) {
      if (levelIndex <= 2) process.stderr.write(formatLine("warn", component, data, msg) + "\n");
    },
    error(data: LogData, msg: string) {
      process.stderr.write(formatLine("error", component, data, msg) + "\n");
    },
  };
}
