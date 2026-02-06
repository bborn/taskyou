# ty-email Launch Tweet

## Tweet

Just shipped ty-email: manage your AI coding tasks from your phone's email client.

Email "Fix the checkout page" → Claude classifies intent → task created → AI agent ships the fix → you get a reply with the result.

Your inbox is now a task queue.

github.com/bborn/taskyou

## Thread (optional follow-ups)

### Reply 1

How it works:

1. Email yourname+ty@gmail.com
2. Gmail filter routes it to ty-email
3. Claude classifies your intent (create task, provide input, check status)
4. ty CLI executes the action
5. You get a reply email with confirmation

No app needed. Just email.

### Reply 2

The best part: when an AI agent gets stuck and needs your input, you get an email. Just reply with your answer — ty-email routes it to the right task automatically.

Async collaboration between you and your AI agents, from anywhere.

### Reply 3

Setup takes 2 minutes:

```
ty-email init   # interactive wizard
ty-email serve  # run the daemon
```

Works with Gmail via IMAP. Sender whitelist for security. Claude handles intent classification.

Open source: github.com/bborn/taskyou/tree/main/extensions/ty-email
