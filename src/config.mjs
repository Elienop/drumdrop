import { homedir } from 'node:os';
import { join } from 'node:path';

// Resolved at call time so tests can override via DRUMDROP_CONFIG_DIR.
export function configDir() {
  return process.env.DRUMDROP_CONFIG_DIR || join(homedir(), '.config', 'drumdrop');
}
export const secretKeyPath = () => join(configDir(), 'secret.key');
export const credsPath = () => join(configDir(), 'credentials.enc');
export const cookiePath = () => join(configDir(), 'session.cookie');
