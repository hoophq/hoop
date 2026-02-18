# HOOP WebApp Development Guidelines

## Code Style Guidelines
- **ClojureScript**: Follow idiomatic ClojureScript style
- **Namespaces**: Organized by feature (connections, webclient, components)
- **Components**: Prefer small, reusable components in `webapp.components.*`
  - Use full Reagent (functional components) instead of `create-class`
  - Follow colocation pattern - keep related code close together (see newer code examples)
- **State Management**: Use re-frame for app state with proper subscriptions/events
  - Avoid dereferencing atoms (`@`) in the first layer - add atoms in the first layer and call them in the second layer
  - Keep code organization clean by maintaining proper atom usage patterns
- **CSS**: Use Tailwind utility classes, see tailwind.config.js
- **Error Handling**: Use re-frame effects for async error handling
- **Naming**: Use kebab-case for functions/variables, PascalCase for React components
- **Imports**: Group by source (external JS libraries first, then ClojureScript)
- **UI Components**: Prefer using existing components from the codebase
- **Modal/Dialog**: Use app's modal system with re-frame events (:modal->open/:modal->close)
- **Accessibility**: Ensure basic W3C accessibility standards
  - Add proper `label` attributes for form inputs
  - Include `alt` text for images
  - Follow W3C best practices for users with visual impairments

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

## Common Patterns
- New features should follow existing patterns in similar components
- Use existing UI components when possible before creating new ones
- Follow existing event/subscription patterns for state management
- For forms and tables, refer to guardrails and jira_templates implementations

## Response Guidelines
- Prefer concise and objective responses
- Focus on project architecture and patterns (ClojureScript, Reagent, re-frame, Radix UI)
- Use real code examples from the project whenever possible
- When suggesting changes, consider the impact on existing structure
- Maintain colocation pattern and keep related code organized together
