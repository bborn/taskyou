# Fieldwork storefront

A dependency-free product page. Open index.html in a browser. Run checks with `node --test`.

Product measurements (chest circumference / body length), cm:
S: 104 / 71; M: 110 / 73; L: 116 / 75; XL: 122 / 77.

Keep native controls, keyboard access, and the existing visual style. Add behavior tests for any new size-conversion logic. Work locally; do not publish or push.

## Try it with TaskYou

Install TaskYou, Git, tmux, Node.js (18 or later), and an authenticated coding agent.
Then run `bash start.sh`. It prepares a local Git repository and opens TaskYou.
Accept the detected project. Press **n**, choose your agent, and use this request:

> Add a size guide to product pages. Show measurements in cm and inches. Keep it usable on mobile. Add tests for the conversion and run node --test. Do not push or open a PR.

Save, select the task, and press **x**. Open the card to watch the executor and use
its shell to run `node --test`. You can open the task worktree's `index.html` in a
browser to try the result. The starting page deliberately has no size guide.

Nothing executes until you queue a task. There is no Git remote. Agent usage uses
your existing account. `start.sh` does not install dependencies or change global
Git settings.
