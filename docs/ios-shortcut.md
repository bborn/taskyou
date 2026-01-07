# iOS Shortcut for GitHub Task Queue

Optional but nice—create tasks via Siri.

## Method 1: Use GitHub App (Easiest)

Just use the GitHub mobile app:
1. Open GitHub app
2. Go to your `task-queue` repo
3. Tap Issues → New Issue
4. Type your task

This works well and needs no setup.

## Method 2: Siri Shortcut (Faster)

Create a shortcut that calls the GitHub API directly.

### Setup

1. **Get a GitHub Personal Access Token**
   - Go to GitHub → Settings → Developer settings → Personal access tokens → Tokens (classic)
   - Generate new token with `repo` scope
   - Copy the token

2. **Create the Shortcut**

   Open Shortcuts app and create new shortcut with these actions:

   **Action 1: Ask for Input**
   - Prompt: "What's the task?"
   - Input Type: Text

   **Action 2: URL**
   - `https://api.github.com/repos/YOUR_USERNAME/task-queue/issues`

   **Action 3: Get Contents of URL**
   - Method: POST
   - Headers:
     - `Authorization`: `Bearer YOUR_GITHUB_TOKEN`
     - `Accept`: `application/vnd.github.v3+json`
     - `Content-Type`: `application/json`
   - Request Body: JSON
     ```json
     {
       "title": "[Input from Ask]",
       "labels": ["status:queued"]
     }
     ```

   **Action 4: Get Dictionary Value**
   - Key: `html_url`

   **Action 5: Show Notification**
   - Title: "Task Created ✓"
   - Body: [Dictionary Value]

3. **Name it** "Add Task"

4. **Test** by running the shortcut

### With Type Selection

More advanced version that lets you pick the task type:

1. **Ask for Input** → "What's the task?"

2. **Choose from Menu**
   - Prompt: "Type?"
   - Options: Code, Writing, Thinking, Skip

3. **If** (Menu Result is "Code")
   - **Text**: `["status:queued", "type:code"]`
4. **Otherwise If** (Menu Result is "Writing")
   - **Text**: `["status:queued", "type:writing"]`
5. **Otherwise If** (Menu Result is "Thinking")
   - **Text**: `["status:queued", "type:thinking"]`
6. **Otherwise**
   - **Text**: `["status:queued"]`
7. **End If**

8. **URL** → GitHub API URL

9. **Get Contents of URL**
   - Body JSON with labels set to the If result

10. **Show Notification**

### With Project Selection

Even more options:

1. Ask for Input (task description)
2. Choose from Menu: Offerlab, InfluenceKit, Personal, Skip
3. Choose from Menu: Code, Writing, Thinking, Skip
4. Build labels array based on selections
5. POST to GitHub API
6. Show notification

## Voice Workflow

Once set up:

1. "Hey Siri, Add Task"
2. Siri: "What's the task?"
3. You: "Fix the user login redirect bug"
4. [Optional type/project selection]
5. Notification: "Task Created ✓"

## Tips

- **Pin to Home Screen**: Long press shortcut → Add to Home Screen
- **Action Button**: iPhone 15 Pro+ can trigger from Action Button
- **Apple Watch**: Shortcuts sync to watch
- **Widget**: Add Shortcuts widget for one-tap access

## Alternative: Drafts App

If you use Drafts:

1. Create a Drafts action that calls the GitHub API
2. Quick capture → run action → task created
3. Works offline (queues until connected)

## Alternative: Working Copy App

If you use Working Copy (Git app for iOS):

1. Open task-queue repo in Working Copy
2. Use the built-in issue creation
3. Full label support
