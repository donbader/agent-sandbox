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

/** Structured logger injected into plugins by the plugin system. */
export interface PluginLogger {
  debug(data: Record<string, unknown>, msg: string): void;
  info(data: Record<string, unknown>, msg: string): void;
  warn(data: Record<string, unknown>, msg: string): void;
  error(data: Record<string, unknown>, msg: string): void;
  /** Create a child logger with an additional component suffix. */
  child(subcomponent: string): PluginLogger;
}

/** Create a PluginLogger backed by pino, with child() support for sub-components. */
export function createPluginLogger(component: string): PluginLogger {
  function wrap(pinoLogger: pino.Logger, comp: string): PluginLogger {
    return {
      debug(data, msg) { pinoLogger.debug(data, msg); },
      info(data, msg) { pinoLogger.info(data, msg); },
      warn(data, msg) { pinoLogger.warn(data, msg); },
      error(data, msg) { pinoLogger.error(data, msg); },
      child(subcomponent: string) {
        const childComp = `${comp}:${subcomponent}`;
        return wrap(pinoLogger.child({ component: childComp }), childComp);
      },
    };
  }
  return wrap(logger.child({ component }), component);
}
