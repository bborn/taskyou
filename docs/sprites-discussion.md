# Sprites Integration: A Discussion

*A dialogue between two perspectives on adopting Sprites for cloud-based Claude execution*

---

## The Proposal

Replace or augment the current `taskd` cloud execution model with Sprites - Fly.io's managed sandbox environments designed for AI agents.

---

## Alex (Advocate for Sprites)

### Opening Statement

The current `taskd` approach requires users to provision and maintain their own cloud server. That's a significant barrier. You need to:

1. Have a VPS or cloud instance running 24/7
2. Configure SSH, systemd, and security
3. Keep the server updated and patched
4. Pay for idle time when no tasks are running
5. Manage SSH keys and GitHub credentials on the server

With Sprites, we eliminate all of that. One API key, and you're running Claude in isolated VMs in the cloud. The `task cloud init` wizard becomes `task cloud login` - enter your Sprites token, done.

### On the Fly.io Dependency

Yes, users would need a Fly.io account. But consider what they need *now* for cloud execution:

- A cloud provider account (AWS, DigitalOcean, Hetzner, etc.)
- SSH access configured
- A running server ($5-20/month minimum, always on)
- Technical knowledge to debug server issues

Sprites trades one dependency for another - but it's a *managed* dependency. Fly.io handles:
- VM provisioning
- Security patching
- Network isolation
- Resource scaling

The $30 free trial credit is enough for ~65 hours of Claude sessions. That's plenty to evaluate whether this works for your workflow.

### On Cost

Let's do the math:

**Current cloud model (taskd on a VPS):**
- Minimum viable server: ~$5/month = $60/year
- That's 24/7 whether you use it or not
- Plus your time maintaining it

**Sprites model:**
- 4-hour Claude session: ~$0.46
- 130 four-hour sessions = $60
- You only pay for what you use

If you're running fewer than 130 substantial Claude sessions per year, Sprites is cheaper. If you're running more, the VPS makes sense - but at that volume, you probably want dedicated infrastructure anyway.

### On Isolation and Security

This is where Sprites really shines. Currently:

- Claude runs with your user's full permissions
- It can access any file your user can access
- Network access is unrestricted
- A malicious prompt could theoretically exfiltrate data

With Sprites:

- Each task runs in a hardware-isolated VM
- Network policies can whitelist only necessary domains
- The sprite gets deleted after task completion
- No persistent access to your local machine

This is *meaningful* security improvement. We're running an AI agent that can execute arbitrary code. Isolation matters.

### On Complexity vs. Simplicity

The current architecture is elegant for local use. But "run `taskd` on a server" introduces real complexity:

- SSH tunneling for the TUI
- Database synchronization concerns
- Server maintenance burden
- Debugging remote issues

Sprites simplifies this: your local `task` daemon orchestrates remote execution via a REST API. The complexity is Fly.io's problem, not yours.

---

## Jordan (Skeptic / Devil's Advocate)

### Opening Statement

I appreciate the vision, but I have concerns about coupling core functionality to a third-party service. Let me push back on several points.

### On the Fly.io Dependency

This isn't just "a dependency" - it's a *hard* dependency on a specific vendor for core functionality. Consider:

1. **What if Fly.io raises prices?** The $0.07/CPU-hour could become $0.14. Our users are locked in.

2. **What if Fly.io goes down?** Their outages become our outages. Users can't execute cloud tasks at all.

3. **What if Fly.io discontinues Sprites?** It's a relatively new product. If it doesn't work out for them, our users are stranded.

4. **What about enterprise users?** Many companies won't approve sending code to a third-party service. They have their own cloud infrastructure.

The current model (bring your own server) is vendor-agnostic. It works on any Linux box. That's a feature, not a bug.

### On the "Simplicity" Argument

Yes, `task cloud init` is complex. But it's *one-time* complexity that results in infrastructure you control. With Sprites:

- Every task execution depends on network connectivity to Fly.io
- Every task execution depends on Sprites API being available
- You're sending your code and prompts through their infrastructure
- You're trusting their isolation claims

"Simple" sometimes means "someone else's complexity that you can't inspect or control."

### On Local Development Experience

The current tmux model has a massive advantage: you can `tmux attach` and interact with Claude in real-time. You can see exactly what it's doing. You can type corrections mid-task.

With Sprites, we lose that direct interactivity. Yes, we can stream output, but:

- There's network latency on every keystroke
- Attaching to a remote sprite is more complex than `tmux attach`
- The debugging experience degrades

For many users, the ability to watch and intervene is a core feature.

### On Cost (A Different Perspective)

The $0.46 per 4-hour session sounds cheap, but consider actual usage patterns:

- Developer runs 5-10 tasks per day during active development
- Many tasks are quick iterations, but the sprite still needs to spin up
- Startup time (~2-5 seconds) adds friction to the workflow
- Failed tasks still cost money

A $5/month VPS runs unlimited tasks with zero marginal cost. For active users, that math flips quickly.

Also: the VPS runs 24/7, which means it can:
- Run scheduled tasks
- Process webhooks
- Serve as a persistent development environment

A sprite is ephemeral by design.

### On Security (The Other Side)

The security argument cuts both ways:

1. **You're sending code to Fly.io's infrastructure.** For open source projects, maybe that's fine. For proprietary code, that's a compliance conversation.

2. **Sprites need git credentials.** Either you're passing tokens to each sprite (security risk) or setting up some credential proxy (complexity).

3. **Hook callbacks need a reachable endpoint.** Either your local machine needs to be addressable from the internet (security risk) or you're polling (latency).

The current model keeps everything on infrastructure you control.

### On the "Right Tool" Question

What problem are we actually solving?

- If users want isolation, we could use local containers (Docker, Podman)
- If users want cloud execution, they can already use taskd
- If users want pay-per-use, they could run taskd on a spot instance

Sprites solves a specific problem: "I want managed, isolated cloud execution with minimal setup." Is that problem common enough to justify the integration complexity and vendor lock-in?

---

## Alex's Rebuttal

### On Vendor Lock-in

Fair point, but we're not proposing to *replace* local execution - we're *adding* an option. The architecture would be:

```
task execute --local    # Current tmux model (default)
task execute --sprite   # New Sprites model (opt-in)
task execute --cloud    # Current taskd model (still works)
```

Users choose based on their needs. Vendor lock-in only applies if they choose the Sprites path.

### On Enterprise Concerns

Enterprise users probably aren't using `task` as-is anyway - they'd fork it and customize. But Sprites does have SOC 2 compliance (Fly.io is enterprise-ready). Still, point taken: we should keep taskd as an option.

### On Interactivity

This is my biggest concession. The tmux attach experience is genuinely better for interactive debugging. We could mitigate with:

- Rich output streaming to the TUI
- A `task sprite attach` command that opens a shell to the sprite
- Keeping local execution as the default for development

But yes, the experience is different.

### On the Core Question

The problem we're solving: "Cloud execution without server management."

The current answer (`taskd`) works, but requires DevOps skills. Sprites lowers the barrier dramatically. That might expand who can use cloud execution from "people comfortable managing servers" to "anyone with a Fly.io account."

---

## Jordan's Rebuttal

### On "It's Optional"

Optional features still have costs:

1. **Maintenance burden:** Two execution paths to maintain, test, and debug
2. **Documentation complexity:** Users need to understand which mode to use when
3. **Cognitive overhead:** "Should I use local, sprite, or cloud?"

Every feature we add is a feature we maintain forever.

### On the User Base

Let's be honest about who uses `task`:

- Developers comfortable with CLI tools
- People who can navigate git worktrees
- Likely comfortable with basic server setup

Is "I want cloud execution but can't manage a VPS" actually a common user profile? Or are we solving a theoretical problem?

### On Alternatives

Before committing to Sprites, shouldn't we consider:

1. **Improve taskd setup:** Make `task cloud init` even simpler, more reliable
2. **Docker-based local isolation:** Same security benefits, no external dependency
3. **Support multiple cloud backends:** Abstract an interface, let users plug in Sprites OR their own runners

Option 3 is more work, but results in better architecture. If we build a proper "remote executor" abstraction, Sprites becomes one implementation - not the only one.

---

## Synthesis: Where Does This Leave Us?

### Points of Agreement

1. **Cloud execution is valuable** - Both perspectives agree remote execution has its place
2. **Current taskd setup is complex** - There's room for improvement
3. **Isolation matters** - Running arbitrary AI-generated code in isolation is a good idea
4. **Local should stay default** - The tmux experience is core to the product

### Points of Contention

1. **Is vendor dependency acceptable?** - Depends on user priorities
2. **Is the simplicity worth the trade-offs?** - Subjective
3. **Is this solving a real problem?** - Needs user research

### Possible Paths Forward

**Path A: Full Sprites Integration**
- Add Sprites as a first-class execution option
- Accept the Fly.io dependency
- Keep local and taskd as alternatives
- Target: Users who want managed cloud without server ops

**Path B: Abstract Remote Executor**
- Define a "RemoteExecutor" interface
- Implement Sprites as one backend
- Also support: Docker, Podman, SSH-to-server
- More work upfront, more flexibility long-term

**Path C: Improve What We Have**
- Make taskd setup more reliable
- Add optional Docker isolation for local execution
- Skip the Sprites dependency entirely
- Focus on polishing existing features

**Path D: Wait and See**
- Document the Sprites option in design docs
- Let users experiment manually if interested
- Revisit if there's demand
- Avoid premature optimization

---

## Open Questions

1. How many users actually want cloud execution today?
2. What's the typical task profile - many short tasks or few long ones?
3. Would users trust sending their code to Fly.io?
4. Is interactive debugging (tmux attach) essential or nice-to-have?
5. What's our maintenance bandwidth for new execution backends?

---

## Conclusion

Both perspectives have merit. The decision ultimately depends on:

- **Target user profile:** How technical are they? What do they value?
- **Project priorities:** Simplicity vs. flexibility? Features vs. maintenance?
- **Risk tolerance:** Is vendor dependency acceptable?

This isn't a clear-cut technical decision - it's a product direction question that deserves user input before we commit significant engineering effort.

---

*Document created for discussion purposes. No decisions have been made.*
