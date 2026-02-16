import { execSync, spawn, ChildProcess } from 'child_process';
import { resolve } from 'path';

export interface HollerMessage {
  v: number;
  id: string;
  from: string;
  to: string;
  ts: number;
  type: string;
  body: string;
  sig: string;
  meta?: Record<string, string>;
  replyTo?: string;
}

export interface Contact {
  alias: string;
  peerId: string;
}

export interface OutboxMessage {
  id: string;
  to: string;
  body: string;
  ts: number;
  retries: number;
  nextRetry: number;
}

export interface HollerOptions {
  binary?: string;
  dir?: string;
}

export interface SendOptions {
  type?: string;
  meta?: Record<string, string>;
  replyTo?: string;
}

export interface PingResult {
  rtt: number;
}

export class Holler {
  private binary: string;
  private dir?: string;

  constructor(options: HollerOptions = {}) {
    this.binary = options.binary || 'holler';
    this.dir = options.dir;
  }

  private execHoller(args: string[]): string {
    const command = [this.binary];
    
    if (this.dir) {
      command.push('--dir', this.dir);
    }
    
    command.push(...args);
    
    try {
      return execSync(command.join(' '), { 
        encoding: 'utf-8',
        stdio: ['pipe', 'pipe', 'pipe']
      }).trim();
    } catch (error: any) {
      throw new Error(`holler command failed: ${error.message}`);
    }
  }

  private execHollerJson<T>(args: string[]): T {
    const output = this.execHoller(args);
    try {
      return JSON.parse(output);
    } catch (error) {
      throw new Error(`Failed to parse holler output as JSON: ${output}`);
    }
  }

  /**
   * Initialize holler identity (first-time setup)
   * @returns PeerID of the newly created identity
   */
  async init(): Promise<string> {
    return this.execHoller(['init']);
  }

  /**
   * Get this agent's PeerID
   * @returns PeerID string
   */
  async id(): Promise<string> {
    try {
      return this.execHoller(['id']);
    } catch (error) {
      // If no identity exists, try to initialize
      return this.init();
    }
  }

  /**
   * Send a message to a peer
   * @param peer PeerID or alias to send to
   * @param message Message content
   * @param options Optional parameters
   */
  async send(peer: string, message: string, options: SendOptions = {}): Promise<void> {
    const args = ['send', peer];
    
    if (options.type) {
      args.push('--type', options.type);
    }
    
    if (options.replyTo) {
      args.push('--reply-to', options.replyTo);
    }
    
    if (options.meta) {
      for (const [key, value] of Object.entries(options.meta)) {
        args.push('--meta', `${key}=${value}`);
      }
    }
    
    args.push(message);
    
    this.execHoller(args);
  }

  /**
   * Listen for incoming messages
   * @param callback Function to call for each received message
   * @returns ChildProcess that can be killed to stop listening
   */
  listen(callback: (message: HollerMessage) => void): ChildProcess {
    const args = [this.binary];
    
    if (this.dir) {
      args.push('--dir', this.dir);
    }
    
    args.push('listen');
    
    const process = spawn(args[0], args.slice(1), {
      stdio: ['pipe', 'pipe', 'pipe']
    });

    process.stdout?.on('data', (data: Buffer) => {
      const lines = data.toString().trim().split('\n');
      
      for (const line of lines) {
        if (line.trim()) {
          try {
            const message: HollerMessage = JSON.parse(line);
            callback(message);
          } catch (error) {
            console.error('Failed to parse message JSON:', line, error);
          }
        }
      }
    });

    process.stderr?.on('data', (data: Buffer) => {
      console.error('holler listen stderr:', data.toString());
    });

    return process;
  }

  /**
   * Get saved contacts
   * @returns Array of contacts with alias and peerId
   */
  async contacts(): Promise<Contact[]> {
    try {
      const output = this.execHoller(['contacts']);
      const contacts: Contact[] = [];
      
      // Parse the contact list format: "alias    peerID" (whitespace separated)
      for (const line of output.split('\n')) {
        const trimmed = line.trim();
        if (trimmed) {
          const parts = trimmed.split(/\s+/);
          if (parts.length >= 2) {
            contacts.push({
              alias: parts[0],
              peerId: parts[parts.length - 1]
            });
          }
        }
      }
      
      return contacts;
    } catch (error) {
      // If no contacts or command fails, return empty array
      return [];
    }
  }

  /**
   * Add a contact
   * @param alias Alias name for the contact
   * @param peerId PeerID of the contact
   */
  async addContact(alias: string, peerId: string): Promise<void> {
    this.execHoller(['contacts', 'add', alias, peerId]);
  }

  /**
   * Remove a contact
   * @param alias Alias of the contact to remove
   */
  async removeContact(alias: string): Promise<void> {
    this.execHoller(['contacts', 'rm', alias]);
  }

  /**
   * Ping a peer to check connectivity and measure RTT
   * @param peer PeerID or alias to ping
   * @returns Promise with RTT in milliseconds
   */
  async ping(peer: string): Promise<PingResult> {
    const output = this.execHoller(['ping', peer]);
    
    // Extract RTT from output (assuming format like "RTT: 123ms" or similar)
    const rttMatch = output.match(/(\d+(?:\.\d+)?)ms/);
    if (rttMatch) {
      return { rtt: parseFloat(rttMatch[1]) };
    }
    
    // If we can't parse RTT, just return that ping succeeded
    return { rtt: 0 };
  }

  /**
   * Get list of discovered DHT peers
   * @returns Array of peer IDs
   */
  async peers(): Promise<string[]> {
    try {
      const output = this.execHoller(['peers']);
      return output.split('\n')
        .map(line => line.trim())
        .filter(line => line.length > 0);
    } catch (error) {
      return [];
    }
  }

  /**
   * Get pending outbox messages
   * @returns Array of outbox messages
   */
  async outbox(): Promise<OutboxMessage[]> {
    try {
      return this.execHollerJson<OutboxMessage[]>(['outbox']);
    } catch (error) {
      return [];
    }
  }

  /**
   * Clear all pending outbox messages
   */
  async clearOutbox(): Promise<void> {
    this.execHoller(['outbox', 'clear']);
  }
}

// Export default instance
export default Holler;