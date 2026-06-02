import pino from "pino";

const level = process.env.LOG_LEVEL || "info";

export const logger = pino({
  level,
  formatters: {
    level(label) {
      return { level: label };
    },
  },
  redact: ["token", "authorization", "*.token", "*.authorization"],
});

export function createLogger(component: string) {
  return logger.child({ component });
}
