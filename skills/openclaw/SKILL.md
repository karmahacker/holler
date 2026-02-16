---
name: openclaw-holler
description: Integrate holler P2P messaging with OpenClaw agents. Send messages, listen for agent communications, manage contacts, and coordinate multi-agent workflows over libp2p.
argument-hint: init | send <peer-id|alias> <message> | listen [--timeout 30] | contacts | broadcast <message> | status
allowed-tools: Bash, Read, Write, NodeJS
---

# OpenClaw Holler Integration

## Prerequisites

1. **holler binary**: Must be installed and available in PATH
2. **Node.js**: Required for the TypeScript wrapper
3. **holler-js package**: The TypeScript wrapper should be built and available

Install holler binary:
```bash
go install github.com/1F47E/holler@latest
```

Build the TypeScript wrapper:
```bash
cd /path/to/holler/js
npm install && npm run build
```

## Parsing $ARGUMENTS

| Pattern | Action |
|---------|--------|
| `init` | Initialize OpenClaw agent with holler identity |
| `send <target> <message>` | Send message to another agent |
| `listen` | Start listening for agent messages (interactive) |
| `listen --timeout <seconds>` | Listen for specified duration |
| `contacts` | List known agent contacts |
| `contacts add <alias> <peer-id>` | Add an agent contact |
| `contacts rm <alias>` | Remove an agent contact |
| `broadcast <message>` | Send message to all known agents |
| `status` | Show agent identity and connection status |
| `peers` | List discovered agents on the network |
| `outbox` | Show pending undelivered messages |
| (empty) | Show agent PeerID and basic status |

## Initial Setup

Before using holler integration, initialize the agent:

```javascript
const { Holler } = require('./path/to/holler/js/lib');

async function initializeAgent() {
  const holler = new Holler();
  const peerId = await holler.id();
  
  console.log(`OpenClaw agent initialized with PeerID: ${peerId}`);
  
  // Save agent identity for future use
  await writeFile('~/.openclaw/holler-agent-id.txt', peerId);
  
  return peerId;
}
```

## Agent-to-Agent Messaging

### Send a message to another agent

```javascript
const holler = new Holler();

async function sendToAgent(targetPeer, message) {
  try {
    await holler.send(targetPeer, JSON.stringify({
      type: 'agent-message',
      from: 'openclaw',
      timestamp: Date.now(),
      content: message
    }));
    console.log(`Message sent to ${targetPeer}: ${message}`);
  } catch (error) {
    console.error(`Failed to send message: ${error.message}`);
    
    // Check if message was queued for later delivery
    const outbox = await holler.outbox();
    if (outbox.length > 0) {
      console.log(`Message queued for delivery when peer comes online`);
    }
  }
}
```

### Listen for agent communications

```javascript
async function listenForAgents(timeoutSeconds = 0) {
  const holler = new Holler();
  
  console.log("Listening for agent messages...");
  
  const listener = holler.listen((message) => {
    try {
      const parsed = JSON.parse(message.body);
      
      if (parsed.type === 'agent-message') {
        console.log(`Agent message from ${message.from}:`);
        console.log(`  Content: ${parsed.content}`);
        console.log(`  Timestamp: ${new Date(parsed.timestamp).toISOString()}`);
        
        // Auto-acknowledge agent messages
        holler.send(message.from, JSON.stringify({
          type: 'ack',
          replyTo: message.id,
          timestamp: Date.now()
        }));
      }
      
    } catch (error) {
      console.log(`Raw message from ${message.from}: ${message.body}`);
    }
  });
  
  // Set timeout if specified
  if (timeoutSeconds > 0) {
    setTimeout(() => {
      listener.kill();
      console.log("Stopped listening for messages");
    }, timeoutSeconds * 1000);
  }
  
  return listener;
}
```

## Contact Management for Agents

```javascript
async function manageAgentContacts(action, alias = '', peerId = '') {
  const holler = new Holler();
  
  switch (action) {
    case 'list':
      const contacts = await holler.contacts();
      console.log("Known agents:");
      for (const contact of contacts) {
        console.log(`  ${contact.alias}: ${contact.peerId}`);
      }
      break;
      
    case 'add':
      await holler.addContact(alias, peerId);
      console.log(`Added agent ${alias} (${peerId})`);
      break;
      
    case 'remove':
      await holler.removeContact(alias);
      console.log(`Removed agent ${alias}`);
      break;
  }
}
```

## Multi-Agent Workflows

### Broadcast to all known agents

```javascript
async function broadcastToAgents(message) {
  const holler = new Holler();
  const contacts = await holler.contacts();
  
  console.log(`Broadcasting to ${contacts.length} agents...`);
  
  const broadcast = {
    type: 'broadcast',
    from: 'openclaw',
    timestamp: Date.now(),
    content: message
  };
  
  for (const contact of contacts) {
    try {
      await holler.send(contact.peerId, JSON.stringify(broadcast));
      console.log(`✓ Sent to ${contact.alias}`);
    } catch (error) {
      console.log(`✗ Failed to send to ${contact.alias}: ${error.message}`);
    }
  }
}
```

### Coordinate task distribution

```javascript
async function distributeTask(taskDescription, targetAgents = []) {
  const holler = new Holler();
  const taskId = `task-${Date.now()}`;
  
  const taskMessage = {
    type: 'task-assignment',
    id: taskId,
    description: taskDescription,
    timestamp: Date.now(),
    deadline: Date.now() + (60 * 60 * 1000) // 1 hour
  };
  
  const contacts = await holler.contacts();
  const targets = targetAgents.length > 0 
    ? contacts.filter(c => targetAgents.includes(c.alias))
    : contacts;
  
  console.log(`Distributing task ${taskId} to ${targets.length} agents`);
  
  for (const agent of targets) {
    await holler.send(agent.peerId, JSON.stringify(taskMessage));
    console.log(`Task assigned to ${agent.alias}`);
  }
  
  return taskId;
}
```

## Integration Patterns

### OpenClaw Session Handler

```javascript
class OpenClawHollerSession {
  constructor() {
    this.holler = new Holler();
    this.agentId = null;
    this.activeListeners = [];
  }
  
  async initialize() {
    this.agentId = await this.holler.id();
    console.log(`OpenClaw Holler session started: ${this.agentId}`);
  }
  
  async handleCommand(command, args) {
    switch (command) {
      case 'send':
        return await this.sendMessage(args[0], args.slice(1).join(' '));
      
      case 'listen':
        return await this.startListening(args);
      
      case 'broadcast':
        return await this.broadcastMessage(args.join(' '));
      
      case 'contacts':
        return await this.manageContacts(args);
      
      default:
        return `Unknown command: ${command}`;
    }
  }
  
  async cleanup() {
    for (const listener of this.activeListeners) {
      listener.kill();
    }
    this.activeListeners = [];
  }
}
```

### Persistent Agent Discovery

```javascript
async function maintainAgentDirectory() {
  const holler = new Holler();
  const directoryFile = '~/.openclaw/agent-directory.json';
  
  // Load existing directory
  let directory = {};
  try {
    const content = await readFile(directoryFile);
    directory = JSON.parse(content);
  } catch (error) {
    directory = { agents: {}, lastUpdate: 0 };
  }
  
  // Discover new agents
  const peers = await holler.peers();
  const timestamp = Date.now();
  
  for (const peerId of peers) {
    if (!directory.agents[peerId]) {
      directory.agents[peerId] = {
        firstSeen: timestamp,
        lastSeen: timestamp,
        alias: null
      };
    } else {
      directory.agents[peerId].lastSeen = timestamp;
    }
  }
  
  directory.lastUpdate = timestamp;
  await writeFile(directoryFile, JSON.stringify(directory, null, 2));
  
  console.log(`Updated agent directory: ${peers.length} active agents`);
}
```

## Error Handling

Always handle network failures gracefully:

```javascript
async function robustSend(peer, message, retries = 3) {
  const holler = new Holler();
  
  for (let attempt = 1; attempt <= retries; attempt++) {
    try {
      await holler.send(peer, message);
      return true;
    } catch (error) {
      console.log(`Send attempt ${attempt} failed: ${error.message}`);
      
      if (attempt === retries) {
        // Check if message was queued
        const outbox = await holler.outbox();
        if (outbox.some(msg => msg.to === peer)) {
          console.log("Message queued for later delivery");
          return true;
        }
        throw error;
      }
      
      // Wait before retry
      await new Promise(resolve => setTimeout(resolve, 1000 * attempt));
    }
  }
}
```

## Security Considerations

- Messages are encrypted and signed automatically by holler
- Always validate incoming message structure before processing
- Use aliases instead of exposing full PeerIDs in logs
- Consider implementing rate limiting for message processing
- Store sensitive data separately from holler's data directory

## Performance Tips

- Use `holler listen --daemon` for long-running background listening
- Batch multiple sends to the same peer when possible
- Regularly clean up the outbox to prevent memory bloat
- Use contact aliases to reduce bandwidth and improve readability