# holler-js

TypeScript/JavaScript wrapper for [holler](https://github.com/1F47E/holler) - P2P encrypted messaging for AI agents.

## Installation

```bash
npm install holler-js
```

**Prerequisites**: The `holler` binary must be available in your PATH or specified via options. Install it with:

```bash
go install github.com/1F47E/holler@latest
```

## Quick Start

```typescript
import { Holler } from 'holler-js';

const holler = new Holler();

// Initialize identity (first-time setup)
const peerId = await holler.init();
console.log('My PeerID:', peerId);

// Send a message
await holler.send('12D3KooW...', 'Hello from JavaScript!');

// Listen for messages
const listener = holler.listen((message) => {
  console.log('Received:', message.body, 'from', message.from);
  
  // Reply back
  holler.send(message.from, `Got your message: ${message.body}`);
});

// Stop listening after 30 seconds
setTimeout(() => listener.kill(), 30000);
```

## API Reference

### Constructor

```typescript
const holler = new Holler(options?)
```

**Options:**
- `binary?: string` - Path to holler binary (default: `'holler'`)
- `dir?: string` - Custom data directory for identity/storage

### Identity Methods

#### `init(): Promise<string>`

Generate a new identity (first-time setup). Returns the PeerID.

#### `id(): Promise<string>`

Get this agent's PeerID. Auto-initializes if no identity exists.

### Messaging Methods

#### `send(peer, message, options?): Promise<void>`

Send a message to a peer.

**Parameters:**
- `peer: string` - PeerID or contact alias
- `message: string` - Message content
- `options?: SendOptions`
  - `type?: string` - Message type
  - `meta?: Record<string,string>` - Metadata key-value pairs
  - `replyTo?: string` - Message ID this is replying to

#### `listen(callback): ChildProcess`

Listen for incoming messages. Returns a ChildProcess that can be killed to stop listening.

**Callback receives:**
```typescript
interface HollerMessage {
  v: number;           // Protocol version
  id: string;          // Message UUID
  from: string;        // Sender PeerID
  to: string;          // Recipient PeerID
  ts: number;          // Unix timestamp
  type: string;        // Message type
  body: string;        // Message content
  sig: string;         // Signature
  meta?: Record<string,string>;  // Metadata
  replyTo?: string;    // Reply-to message ID
}
```

### Contact Management

#### `contacts(): Promise<Contact[]>`

Get saved contacts.

#### `addContact(alias, peerId): Promise<void>`

Save a contact with an alias.

#### `removeContact(alias): Promise<void>`

Remove a saved contact.

### Network Methods

#### `ping(peer): Promise<{ rtt: number }>`

Ping a peer and measure round-trip time.

#### `peers(): Promise<string[]>`

Get list of discovered DHT peers.

### Outbox Methods

#### `outbox(): Promise<OutboxMessage[]>`

Get pending undelivered messages.

#### `clearOutbox(): Promise<void>`

Clear all pending messages.

## Examples

### Agent-to-Agent Communication

```typescript
import { Holler } from 'holler-js';

// Agent A
const agentA = new Holler({ dir: '/tmp/agent-a' });
await agentA.init();

const peerIdA = await agentA.id();
console.log('Agent A PeerID:', peerIdA);

// Agent B  
const agentB = new Holler({ dir: '/tmp/agent-b' });
await agentB.init();

const peerIdB = await agentB.id();
console.log('Agent B PeerID:', peerIdB);

// Agent A listens
agentA.listen((message) => {
  console.log('Agent A received:', message.body);
  agentA.send(message.from, 'Acknowledged!');
});

// Agent B sends
await agentB.send(peerIdA, 'Hello Agent A!');
```

### Contact Management

```typescript
const holler = new Holler();

// Add contacts
await holler.addContact('alice', '12D3KooWAbc...');
await holler.addContact('bob', '12D3KooWDef...');

// List contacts
const contacts = await holler.contacts();
console.log('Contacts:', contacts);

// Send to alias instead of full PeerID
await holler.send('alice', 'Hello Alice!');
```

### Structured Messaging

```typescript
interface TaskMessage {
  type: 'task' | 'result';
  id: string;
  data: any;
}

const holler = new Holler();

// Send structured data
const task: TaskMessage = {
  type: 'task',
  id: 'task-123',
  data: { action: 'summarize', text: 'Long text here...' }
};

await holler.send('worker-agent', JSON.stringify(task), {
  type: 'task',
  meta: { priority: 'high' }
});

// Listen and process
holler.listen((message) => {
  if (message.type === 'task') {
    const task: TaskMessage = JSON.parse(message.body);
    // Process task...
    
    const result: TaskMessage = {
      type: 'result',
      id: task.id,
      data: { summary: 'Task completed successfully' }
    };
    
    holler.send(message.from, JSON.stringify(result), {
      type: 'result',
      replyTo: message.id
    });
  }
});
```

### Error Handling

```typescript
const holler = new Holler();

try {
  await holler.send('offline-peer', 'Hello');
  console.log('Message sent successfully');
} catch (error) {
  console.error('Failed to send message:', error.message);
  
  // Check outbox for queued messages
  const pending = await holler.outbox();
  console.log('Pending messages:', pending.length);
}
```

## Integration with OpenClaw

This wrapper is designed to work seamlessly with OpenClaw agents:

```typescript
import { Holler } from 'holler-js';

class OpenClawHollerAgent {
  private holler: Holler;
  
  constructor() {
    this.holler = new Holler();
  }
  
  async initialize() {
    const peerId = await this.holler.id();
    console.log(`OpenClaw agent initialized with PeerID: ${peerId}`);
    
    // Start listening for messages
    this.holler.listen((message) => {
      this.handleMessage(message);
    });
  }
  
  private async handleMessage(message: HollerMessage) {
    // Process incoming agent messages
    console.log(`Received from ${message.from}: ${message.body}`);
    
    // Auto-acknowledge
    await this.holler.send(message.from, 'Message received', {
      replyTo: message.id
    });
  }
  
  async broadcastStatus(status: string) {
    const contacts = await this.holler.contacts();
    for (const contact of contacts) {
      await this.holler.send(contact.peerId, `Status update: ${status}`);
    }
  }
}
```

## License

MIT