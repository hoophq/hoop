# HOOP WebApp Development Guidelines

## Build Commands
- Development: `npm run dev:hoop-ui` (runs shadow-cljs and postcss watchers)
- Production build: `npm run release:hoop-ui`
- Tests: `npx shadow-cljs watch browser-test` and view at http://localhost:8290
- Single test: No specific command, run browser-test and filter in browser
- Docker: `docker-compose up --build`

## Code Style Guidelines
- **ClojureScript**: Follow idiomatic ClojureScript style
- **Namespaces**: Organized by feature (connections, webclient, components)
- **Components**: Prefer small, reusable components in `webapp.components.*`
- **State Management**: Use re-frame for app state with proper subscriptions/events
- **CSS**: Use Tailwind utility classes, see tailwind.config.js
- **Error Handling**: Use re-frame effects for async error handling
- **Naming**: Use kebab-case for functions/variables, PascalCase for React components
- **Imports**: Group by source (external JS libraries first, then ClojureScript)
- **UI Components**: Prefer using existing components from the codebase
- **Modal/Dialog**: Use app's modal system with re-frame events (:modal->open/:modal->close)

## Radix UI Components
- **Import Pattern**: Import Radix components from "@radix-ui/themes" using the `:refer` syntax
- **Usage Pattern**: Use Reagent interop syntax `[:> Component {}]` for Radix components
- **Common Components**:
  - `Box` - For layout containers with spacing (e.g., `:class "space-y-radix-7"`)
  - `Flex` - For flexbox layouts
  - `Grid` - For grid layouts, commonly using span patterns like `span-2`/`span-5`
  - `Table` - Use full table components (`Table.Root`, `Table.Header`, `Table.Body`)
  - `Text/Heading` - For typography with consistent sizing
  - `Badge` - For status indicators
  - `Button` - For actions with consistent styling
  - `Select` - For dropdown selections
- **Styling**: Use Radix's built-in props (`:size`, `:variant`, `:color`) and Tailwind for custom styling
- **Reference Components**: See `webapp.guardrails.*` and `webapp.jira_templates.*` for examples

## Accessibility
- **Baseline**: Ship basic accessibility on every UI change—keyboard navigation and screen-reader support are not optional extras.
- **Keyboard**: Interactive controls must be reachable and operable with Tab / Shift+Tab; preserve a sensible focus order; visible focus states; avoid trapping focus except in modals (then follow the modal pattern and restore focus on close).
- **Screen readers**: Use semantic HTML and Radix primitives where they provide roles/labels; associate labels with inputs; use `aria-*` when semantics alone are insufficient.
- **Images and icons**: Meaningful images need descriptive `alt` text; decorative images/icons should use empty `alt=""` (or equivalent `aria-hidden` when appropriate) so assistive tech can skip them. Do not leave `img` without an intentional alt decision.
- **Mindset**: When implementing UI, default to asking whether keyboard and screen-reader users can complete the same tasks; fix gaps in the same change when feasible.

## Common Patterns
- New features should follow existing patterns in similar components
- Use existing UI components when possible before creating new ones
- Follow existing event/subscription patterns for state management
- For forms and tables, refer to guardrails and jira_templates implementations

## Module Organization
- **Feature Modules**: Organize features in their own directories with local `events.cljs` and `subs.cljs`
- **Structure Pattern**:
  ```
  src/webapp/
  ├── features/
  │   └── feature_name/
  │       ├── events.cljs     # re-frame events
  │       ├── subs.cljs       # re-frame subscriptions
  │       └── views/          # UI components
  └── module_name/
      ├── events.cljs         # Module-specific events
      ├── subs.cljs           # Module-specific subscriptions
      ├── main.cljs           # Main component
      └── *.cljs              # Other module files
  ```
- **Global vs Local**:
  - `/src/webapp/events/` and `/src/webapp/subs.cljs` are for GLOBAL events/subs only
  - Feature/module-specific events and subs should live within their module directory
- **Namespacing**: Use namespaced keywords (e.g., `:module-name/event-name`)
- **Initial State**: Define in `/src/webapp/db.cljs` under the module key
- **Examples**: See `webapp.features.access_request.*`, `webapp.features.runbooks.*`, `webapp.audit_logs.*`