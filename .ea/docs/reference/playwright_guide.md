# Playwright Plugin — Usage Guide for Claude Code

Experience from using the `@playwright/mcp` plugin with Claude Code for browser automation tasks (OpenAI OAuth, Outlook email reading, form filling).

## Setup

The plugin is installed via Claude Code's plugin system:
```bash
/plugin playwright  # install
/reload-plugins     # activate
```

Underlying MCP server: `npx @playwright/mcp@latest`

## Core Concepts

### Snapshot vs Screenshot
- **`browser_snapshot`** — returns accessibility tree (YAML), best for interacting with elements (has `ref` IDs)
- **`browser_take_screenshot`** — returns visual image, best for debugging layout issues
- **Prefer snapshot** for all interactions — it's faster and gives you element references

### Element References (`ref`)
Every interactive element in a snapshot has a `ref=eNN` identifier. Use these in click/type/etc. calls:
```yaml
- textbox "Email address" [active] [ref=e12]    # → use ref=e12 to type
- button "Continue" [ref=e15] [cursor=pointer]   # → use ref=e15 to click
```

Refs are **ephemeral** — they change after any navigation or DOM update. Always take a fresh snapshot before interacting.

## Tool Usage Patterns

### Basic Flow: navigate → snapshot → interact → wait → snapshot
```
browser_navigate → browser_snapshot → browser_type/click → browser_wait_for → browser_snapshot
```

### Input: `type` vs `fill` (via `run_code`)
- **`browser_type` with `slowly=true`**: Types character by character (`pressSequentially`). Use when JS validation needs keystroke events (e.g., OpenAI login form).
- **`browser_type` with `submit=true`**: Fills and presses Enter — great for login forms.
- **`fill` (via `run_code`)**: Sets value instantly. May not trigger JS validation on some sites.

```
# Recommended for forms with JS validation:
browser_type(ref=e12, text="user@email.com", slowly=true, submit=true)

# Equivalent via run_code:
browser_run_code: await page.getByRole('textbox', { name: 'Email' }).pressSequentially('user@email.com');
```

### Waiting
- **`browser_wait_for(text="...")`** — wait for text to appear on page
- **`browser_wait_for(time=N)`** — wait N seconds (avoid when possible)
- **`waitForURL` (via `run_code`)** — best for page transitions:
  ```js
  await page.waitForURL('**/log-in/password', { timeout: 10000 });
  ```

### Multi-step Operations with `run_code`
For complex flows, **batch multiple steps in a single `run_code` call** — much faster and more reliable than individual tool calls:

```js
async (page) => {
  // Fill email and submit
  const email = page.getByRole('textbox', { name: 'Email address' });
  await email.pressSequentially('user@example.com');
  await email.press('Enter');
  await page.waitForURL('**/password', { timeout: 10000 });

  // Fill password and submit
  const pwd = page.getByRole('textbox', { name: 'Password' });
  await pwd.pressSequentially('mypassword');
  await pwd.press('Enter');
  await page.waitForURL('**/verification', { timeout: 10000 });

  return 'At verification page';
}
```

### Extracting Data from Pages
```js
// Get text content
const text = await page.getByText('Your code is').first().textContent();
const code = text.match(/\d{6}/)?.[0];

// Get URL after redirect (even if page shows error)
const entries = await page.evaluate(() => {
  return performance.getEntriesByType('navigation').map(e => e.name);
});
// entries[0] contains the full redirect URL with query params
```

### Tab Management
```
browser_tabs(action="new")           # open new tab
browser_tabs(action="select", index=0)  # switch to first tab
browser_tabs(action="close", index=1)   # close second tab
browser_tabs(action="list")          # list all tabs
```

## Common Patterns

### Login Flow
```
1. browser_navigate(url)
2. browser_wait_for(text="Email")
3. browser_type(ref=emailField, text=email, slowly=true, submit=true)
4. browser_wait_for(time=3)  // wait for page transition
5. browser_snapshot()  // check new state
6. browser_type(ref=passwordField, text=password, submit=true)
```

### Handling Popups/Interruptions
Microsoft login frequently shows interruption pages. Handle them in `run_code`:
```js
// Skip "Let's protect your account"
const skipLink = page.getByRole('link', { name: /Skip for now/ });
if (await skipLink.isVisible({ timeout: 3000 }).catch(() => false)) {
  await skipLink.click();
  await page.waitForTimeout(2000);
}

// "Stay signed in?" → Yes
const yesBtn = page.getByRole('button', { name: 'Yes' });
if (await yesBtn.isVisible({ timeout: 3000 }).catch(() => false)) {
  await yesBtn.click();
}
```

### Reading Email (Outlook Web)
```js
// Use login_hint to switch accounts without re-login
await page.goto('https://outlook.live.com/mail/0/junkemail?login_hint=user@outlook.com');

// Wait for email list to load
await page.getByText('Your code is').first().waitFor({ state: 'visible', timeout: 15000 });

// Extract code
const text = await page.getByText('Your code is').first().textContent();
const code = text.match(/\d{6}/)?.[0];
```

### Capturing Redirects to localhost
When OAuth redirects to `http://localhost:PORT/callback?code=XXX`, the browser shows an error page. Extract the URL from navigation history:
```js
const entries = await page.evaluate(() => {
  return performance.getEntriesByType('navigation').map(e => e.name);
});
// entries[0] = "http://localhost:1455/auth/callback?code=ac_xxx&state=yyy"
```

## Tips

1. **Snapshot output is huge** — Outlook snapshots can be 5000+ lines. When you only need one piece of info, use `run_code` with `textContent()` instead.

2. **`ref` IDs expire** — After any page change (navigation, AJAX, DOM mutation), old refs become invalid. Take a new snapshot before each interaction, or use `run_code` with role-based selectors.

3. **Browser stays open between calls** — The plugin maintains browser state across tool calls. Cookies, sessions, and tabs persist until `browser_close`.

4. **Headless mode** — For non-interactive batch runs, configure the MCP server with `--headless`:
   ```json
   {
     "playwright": {
       "command": "npx",
       "args": ["@playwright/mcp@latest", "--headless"]
     }
   }
   ```

5. **Headed mode steals focus** — Chromium window grabs focus on every interaction. No fix in headed mode. Use headless for background work, headed only for debugging.

6. **CC Agent + Playwright > Pure Script** — The agent can read snapshots, reason about unexpected page states, and recover from errors. Scripts break on any unexpected popup or layout change.

7. **`run_code` is your best friend** — Combine multiple steps, add conditional logic, and handle errors all in one call. Much more reliable than chaining individual tool calls.
