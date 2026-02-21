# TaskYou Demo Script: Voice-Driven Development

> **Goal:** Show how to use TaskYou with voice dictation to plan, manage, and build a simple "Emoji Mood Tracker" web app — all without touching the keyboard for task management.
>
> **Duration:** ~10 minutes
>
> **Prerequisites:** TaskYou installed and running, Claude Code configured as executor, microphone set up for voice dictation (macOS: Fn Fn, or any dictation tool)

---

## Act 1: Introduce the Board (30 seconds)

- Open TaskYou in your terminal
  ```
  ty
  ```
- Show the **Kanban board** — point out the four columns: Backlog, In Progress, Blocked, Done
- Say: *"This is TaskYou — a task manager built for AI-assisted development. I'm going to build a small web app using mostly my voice to create and manage tasks, and Claude to write the code."*

---

## Act 2: Create Tasks with Voice Dictation (2 minutes)

- Press **`n`** to open the new task form
- **Activate voice dictation** (on Mac: press Fn twice)
- Dictate the first task title:
  > "Create an HTML page with an emoji mood tracker"
- Press **Tab** to move to the description field
- Dictate the description:
  > "Build a single HTML file with inline CSS and JavaScript. Show a row of five emoji buttons representing moods: very sad, sad, neutral, happy, very happy. When the user clicks an emoji, log it with a timestamp and display a history list below the buttons."
- Press **Ctrl+S** to save the task
- Say: *"That's our first task. Now let's break it down."*

- Press **`n`** again to create a second task
- Dictate:
  > "Add a mood statistics chart using CSS bar chart"
- Tab to description, dictate:
  > "After the basic tracker works, add a simple bar chart below the history that shows how many times each mood was selected. Use pure CSS, no libraries."
- Press **Ctrl+S** to save

- Press **`n`** one more time for a third task
- Dictate:
  > "Add local storage so moods persist across page refreshes"
- Tab to description, dictate:
  > "Save the mood history array to local storage. Load it on page load. The chart and history should populate from saved data."
- Press **Ctrl+S** to save

- Say: *"Three tasks in about a minute, all created with my voice. No typing."*

---

## Act 3: Execute the First Task (3 minutes)

- Navigate to the first task using arrow keys or press **`1`**
- Press **`x`** to execute the task
- Say: *"Now I'm sending this task to Claude. It will create a Git worktree, spin up a Claude Code session, and start building."*
- Press **Enter** to open the task detail view
- Watch Claude work in the **executor pane** (left side)
  - Point out: *"Claude is reading the task, exploring the project, and writing the HTML file."*
- While waiting, show the **shell pane** on the right (press **`\`** to toggle it)
  - Say: *"I can also interact with the worktree directly in this shell."*

---

## Act 4: Handle a Block / Provide Input (1 minute)

- If Claude asks a question (task goes to **Blocked** status):
  - Say: *"Claude has a question for me. Let me answer it with my voice."*
  - Press **`r`** to open the retry/feedback form
  - **Activate voice dictation** and speak your answer, for example:
    > "Yes, use these five emojis: the very sad face, the slightly frowning face, the neutral face, the slightly smiling face, and the star struck face. Make the page dark themed with a deep purple background."
  - Press **Ctrl+S** to send the feedback
  - Say: *"I just gave Claude creative direction, all by voice."*

- If Claude completes without blocking:
  - Say: *"Claude finished without needing input. Let's take a look."*

---

## Act 5: Review the Result (1 minute)

- Once the first task shows as **Done**:
  - Press **`o`** to open the worktree in your editor
  - Show the generated HTML file
  - Open it in a browser to demonstrate the working mood tracker
  - Say: *"Fully functional mood tracker — built from a voice-dictated task description."*

---

## Act 6: Chain the Next Task (2 minutes)

- Press **Esc** to go back to the Kanban board
- Navigate to the second task ("Add a mood statistics chart")
- Press **`x`** to execute it
- Say: *"I'm now queuing the next task. Claude will pick up where the first task left off, in the same codebase."*
- Press **Enter** to watch progress
- When done, refresh the browser to show the new bar chart feature
- Say: *"Each task builds on the last. Voice in, working code out."*

---

## Act 7: Use the Command Palette (30 seconds)

- Press **`p`** or **Ctrl+P** to open the command palette
- **Activate voice dictation** and say:
  > "mood tracker"
- Show how it fuzzy-matches to the relevant tasks
- Press **Enter** to jump to a task
- Say: *"The command palette works great with voice too — just speak the task name and it finds it."*

---

## Act 8: Quick Status Update via CLI (30 seconds)

- Switch to a regular terminal (or use the shell pane)
- **Dictate a CLI command:**
  > "ty list"
- Show the task list output
- Then dictate:
  > "ty create add a confetti animation when the user logs a happy mood"
- Show it appear in the TUI
- Say: *"You can also manage tasks from the command line — great for scripting or quick additions."*

---

## Act 9: Execute the Final Task (1 minute)

- Go back to the TUI, navigate to the third task ("Add local storage")
- Press **`x`** to execute
- Once done, demonstrate in the browser:
  - Click some emoji moods
  - Refresh the page
  - Show that the history and chart persist
- Say: *"Data persists across refreshes. Three tasks, three features, all planned and directed with voice."*

---

## Act 10: Wrap Up (30 seconds)

- Show the Kanban board with all three tasks in the **Done** column
- Say: *"To recap — I used TaskYou to:"*
  - *"Plan tasks by speaking them into the new task form"*
  - *"Execute each task with a single keypress, handing it off to Claude"*
  - *"Provide feedback and creative direction using voice dictation"*
  - *"Navigate the board with the command palette using voice"*
  - *"Build a complete web app without writing a single line of code myself"*
- Say: *"That's TaskYou — voice-driven, AI-powered task management for developers."*

---

## Quick Reference: Key Presses Used in This Demo

| Action | Key |
|--------|-----|
| Open TUI | `ty` in terminal |
| New task | `n` |
| Save task form | `Ctrl+S` |
| Execute task | `x` |
| View task details | `Enter` |
| Retry / give feedback | `r` |
| Open in editor | `o` |
| Command palette | `p` or `Ctrl+P` |
| Toggle shell pane | `\` |
| Back to board | `Esc` |
| Activate macOS dictation | `Fn` twice |

---

## Tips for a Smooth Demo

- **Practice the voice dictation** a few times before recording — know where to pause for punctuation
- **Pre-check Claude is working** — run a quick test task before the demo
- **Use a dark terminal theme** — it looks better on camera and matches the TUI styling
- **Keep the browser ready** — have it positioned next to the terminal for split-screen effect
- **If Claude is slow**, fill time by explaining what's happening: "Claude is analyzing the existing code and planning the implementation"
- **If dictation misfires**, just laugh it off — "Voice recognition isn't perfect, but it's faster than typing for task descriptions"
