# Hoop WebApp V2

Modern React-based web application for Hoop.

## Tech Stack

- **React 19** - UI framework
- **Vite** - Build tool and dev server
- **Mantine v8** - Component library
- **Zustand** - State management
- **React Router v7** - Client-side routing
- **Axios** - HTTP client

## Getting Started

### Prerequisites

- Node.js 18+ and npm

### Installation

```bash
npm install
```

### Development

```bash
npm run dev
```

Access the app at `http://localhost:5173`

### Build

```bash
npm run build
```

### Preview Production Build

```bash
npm run preview
```

## Configuration

### Environment Variables

Create a `.env` file based on `.env.sample`:

```bash
# Optional: Custom API endpoint
# If not set, defaults to /api (relative to current domain)
VITE_API_URL=/api
```

## Authentication

This app supports two authentication methods:

1. **Local Auth**: Email/password authentication via `/localauth/login`
2. **OAuth/IDP**: SSO authentication via configured identity providers

The authentication method is automatically detected from the gateway configuration.

### Auth Flow

- Token is stored in localStorage as `jwt-token`
- Token can be received via cookies (`hoop_access_token`) or query params (`?token=xxx`)
- On 401 responses, the app saves the current URL and redirects to login
- After successful auth, redirects back to the saved URL

## Project Structure

See [CLAUDE.md](./CLAUDE.md) for detailed architecture guidelines.

```
src/
├── components/       # Reusable components
├── routes/          # Page components
├── stores/          # Zustand state stores
├── services/        # API services
├── hooks/           # Custom hooks
└── utils/           # Utility functions
```

## Development Guidelines

Read [CLAUDE.md](./CLAUDE.md) for:
- Architecture patterns
- Code style conventions
- Routing structure
- State management approach
- Authentication implementation details
