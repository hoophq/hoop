# Hoop Web App

This is the code for Hoop Web Application, designed to be a web portal for Hoop main users: DevOps and Software Developers.

Application powered by [re-frame](https://github.com/day8/re-frame).

## Getting Started

### Project Overview

- Architecture:
  [Single Page Application (SPA)](https://en.wikipedia.org/wiki/Single-page_application)
- Languages
  - Front end is [ClojureScript](https://clojurescript.org/) with ([re-frame](https://github.com/day8/re-frame))
- Dependencies
  - UI framework: [re-frame](https://github.com/day8/re-frame)
    ([docs](https://github.com/day8/re-frame/blob/master/docs/README.md),
    [FAQs](https://github.com/day8/re-frame/blob/master/docs/FAQs/README.md)) ->
    [Reagent](https://github.com/reagent-project/reagent) ->
    [React](https://github.com/facebook/react)
  - Client-side routing: [bidi](https://github.com/juxt/bidi) and [pushy](https://github.com/kibu-australia/pushy)
  - CSS framework: [Tailwind](https://tailwindcss.com)
- Build tools
  - CLJS compilation, dependency management, REPL, & hot reload: [`shadow-cljs`](https://github.com/thheller/shadow-cljs)
  - CSS build: [Tailwind JIT](https://tailwindcss.com/docs/just-in-time-mode)
  - Test framework: [cljs.test](https://clojurescript.org/tools/testing)
  - Test runner: [Karma](https://github.com/karma-runner/karma)
- Development tools
  - Debugging: [CLJS DevTools](https://github.com/binaryage/cljs-devtools),
    [`re-frame-10x`](https://github.com/day8/re-frame-10x)

### Editor/IDE

Use your preferred editor or IDE that supports Clojure/ClojureScript development. See
[Clojure tools](https://clojure.org/community/resources#_clojure_tools) for some popular options.

### Environment Setup

1. Install [JDK 8 or later](https://openjdk.java.net/install/) (Java Development Kit)
2. Install [Node.js](https://nodejs.org/) (JavaScript runtime environment) which should include
   [NPM](https://docs.npmjs.com/cli/npm) or if your Node.js installation does not include NPM also install it.
3. Install [Chrome](https://www.google.com/chrome/) or
   [Chromium](https://www.chromium.org/getting-involved/download-chromium) version 59 or later
   (headless test environment) \* For Chromium, set the `CHROME_BIN` environment variable in your shell to the command that
   launches Chromium. For example, in Ubuntu, add the following line to your `.bashrc`:
   `bash export CHROME_BIN=chromium-browser `
4. Clone this repo and open a terminal in the `webapp` project root directory

### Browser Setup

Browser caching should be disabled when developer tools are open to prevent interference with
[`shadow-cljs`](https://github.com/thheller/shadow-cljs) hot reloading.

Custom formatters must be enabled in the browser before
[CLJS DevTools](https://github.com/binaryage/cljs-devtools) can display ClojureScript data in the
console in a more readable way.

#### Chrome/Chromium

1. Open [DevTools](https://developers.google.com/web/tools/chrome-devtools/) (Linux/Windows: `F12`
   or `Ctrl-Shift-I`; macOS: `⌘-Option-I`)
2. Open DevTools Settings (Linux/Windows: `?` or `F1`; macOS: `?` or `Fn+F1`)
3. Select `Preferences` in the navigation menu on the left, if it is not already selected
4. Under the `Network` heading, enable the `Disable cache (while DevTools is open)` option
5. Under the `Console` heading, enable the `Enable custom formatters` option

#### Firefox

1. Open [Developer Tools](https://developer.mozilla.org/en-US/docs/Tools) (Linux/Windows: `F12` or
   `Ctrl-Shift-I`; macOS: `⌘-Option-I`)
2. Open [Developer Tools Settings](https://developer.mozilla.org/en-US/docs/Tools/Settings)
   (Linux/macOS/Windows: `F1`)
3. Under the `Advanced settings` heading, enable the `Disable HTTP Cache (when toolbox is open)`
   option

Unfortunately, Firefox does not yet support custom formatters in their devtools. For updates, follow
the enhancement request in their bug tracker:
[1262914 - Add support for Custom Formatters in devtools](https://bugzilla.mozilla.org/show_bug.cgi?id=1262914).

## Development

### Running the App

Start a temporary local web server, build the app with the `dev` profile, and serve the app, tailwind css server,
browser test runner and karma test runner with hot reload:

```sh
npm install
```

#### Running the Portal Web Hoop

**Configuration**

Copy and paste the `.env.sample` file to a `.env` file and replace the necessary values

````sh
cp .env.sample .env
``**

**Run it**

```sh
npm run dev:hoop-ui
````

Please be patient; it may take over 20 seconds to see any output, and over 40 seconds to complete.

When `[:app] Build completed` appears in the output, browse to
[http://localhost:8280/](http://localhost:8280/).

[`shadow-cljs`](https://github.com/thheller/shadow-cljs) will automatically push ClojureScript code
changes to your browser on save. To prevent a few common issues, see
[Hot Reload in ClojureScript: Things to avoid](https://code.thheller.com/blog/shadow-cljs/2019/08/25/hot-reload-in-clojurescript.html#things-to-avoid).

Opening the app in your browser starts a
[ClojureScript browser REPL](https://clojurescript.org/reference/repl#using-the-browser-as-an-evaluation-environment),
to which you may now connect.

#### Connecting to the browser REPL from your editor

See
[Shadow CLJS User's Guide: Editor Integration](https://shadow-cljs.github.io/docs/UsersGuide.html#_editor_integration).
Note that `npm run watch` runs `npx shadow-cljs watch` for you, and that this project's running build ids is
`app`, `browser-test`, `karma-test`, or the keywords `:app`, `:browser-test`, `:karma-test` in a Clojure context.

Alternatively, search the web for info on connecting to a `shadow-cljs` ClojureScript browser REPL
from your editor and configuration.

For example, in Vim / Neovim with `fireplace.vim`

1. Open a `.cljs` file in the project to activate `fireplace.vim`
2. In normal mode, execute the `Piggieback` command with this project's running build id, `:app`:
   ```vim
   :Piggieback :app
   ```

#### Connecting to the browser REPL from a terminal

1. Connect to the `shadow-cljs` nREPL:

   ```sh
   lein repl :connect localhost:8777
   ```

   The REPL prompt, `shadow.user=>`, indicates that is a Clojure REPL, not ClojureScript.

2. In the REPL, switch the session to this project's running build id, `:app`:
   ```clj
   (shadow.cljs.devtools.api/nrepl-select :app)
   ```
   The REPL prompt changes to `cljs.user=>`, indicating that this is now a ClojureScript REPL.
3. See [`user.cljs`](dev/cljs/user.cljs) for symbols that are immediately accessible in the REPL
   without needing to `require`.

### Running `shadow-cljs` Actions

See a list of [`shadow-cljs CLI`](https://shadow-cljs.github.io/docs/UsersGuide.html#_command_line)
actions:

```sh
npx shadow-cljs --help
```

Please be patient; it may take over 10 seconds to see any output. Also note that some actions shown
may not actually be supported, outputting "Unknown action." when run.

Run a shadow-cljs action on this project's build id (without the colon, just `app`):

```sh
npx shadow-cljs <action> app
```

> Make sure to bump the `package.json` version when making new Pull Requests running `npm version <new-sem-version>`

## Production

Build the app with the `prod` profile:

```sh
npm install # install project dependencies
npm run release:hoop-ui
```

Please be patient; it may take over 15 seconds to see any output, and over 30 seconds to complete.

The `resources/public/js/compiled` directory is created, containing the compiled `app.js` and
`manifest.edn` files.
