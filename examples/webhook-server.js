#!/usr/bin/env node
/**
 * Simple webhook server example for TaskYou events
 * 
 * Install dependencies:
 *   npm install express
 * 
 * Run:
 *   node examples/webhook-server.js
 * 
 * Configure TaskYou:
 *   ty events webhooks add http://localhost:3000/webhook
 *   ty daemon restart
 */

const express = require('express');
const app = express();
const PORT = 3000;

app.use(express.json());

// Webhook endpoint
app.post('/webhook', (req, res) => {
  const event = req.body;
  
  // Log the event
  console.log('\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━');
  console.log(`📥 Event received: ${event.type}`);
  console.log(`   Task #${event.task_id}: ${event.message}`);
  console.log(`   Timestamp: ${event.timestamp}`);
  
  if (event.task) {
    console.log(`   Status: ${event.task.Status}`);
    console.log(`   Project: ${event.task.Project}`);
    console.log(`   Title: ${event.task.Title}`);
  }
  
  if (event.metadata && Object.keys(event.metadata).length > 0) {
    console.log(`   Metadata: ${JSON.stringify(event.metadata)}`);
  }
  
  // Handle specific event types
  switch (event.type) {
    case 'task.completed':
      console.log('   ✅ Task completed successfully!');
      // Could send notification, update external systems, etc.
      break;
      
    case 'task.failed':
      console.log('   ❌ Task failed!');
      // Could alert team, create incident ticket, etc.
      break;
      
    case 'task.blocked':
      console.log('   ⏸️  Task needs attention');
      // Could notify user, update status board, etc.
      break;
  }
  
  // Respond to TaskYou
  res.json({ 
    received: true,
    timestamp: new Date().toISOString()
  });
});

// Health check endpoint
app.get('/health', (req, res) => {
  res.json({ status: 'ok' });
});

app.listen(PORT, () => {
  console.log(`\n🚀 Webhook server listening on http://localhost:${PORT}`);
  console.log(`\nTo configure TaskYou:`);
  console.log(`  ty events webhooks add http://localhost:${PORT}/webhook`);
  console.log(`  ty daemon restart\n`);
  console.log(`Waiting for events...\n`);
});
