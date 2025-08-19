![hero](../github.png)

<h1 align="center">
<b>hoop.dev</b>
</h1>
<p align="center"> 🔒 Secure infrastructure access without complexity or cost 
<br /> <br />

## Hellow world

This is the Hoop Gateway folder. Below are the steps to run it in development. For installation and usage instructions, please refer to the [Hoop documentation](https://hoop.dev/docs).

## Dependencies

- Install Golang 1.20 or later: [https://go.dev/doc/install](https://go.dev/doc/install)
- A PostgreSQL anywhere you want.

## Setup

### Database

If you have a local PostgreSQL database, just create a database named `hoop` and you are done.

Or configure the following environment variable in the .env file to connect to your PostgreSQL database:

```bash
export POSTGRES_DB_URI="postgres://user:password@host:port/dbname?sslmode=disable" # ssl disable makes your life easier at development, but not recommended for production
```

### Configure and run

 - Navigate to `cmd/gateway` folder
 - Copy the `.env.example` file to `.env`, the fields are not required to be changed for local development, but tweak as you wish.
 - Source the `.env` file in your terminal:
   ```sh
   source .env
   ```
 - Run it:
   ```sh
   go run main.go
   ```

### Web UI

Refer to the [`webapp`](https://github.com/hoophq/hoop/tree/main/webapp) folder in the repository for the Web UI and follow the README.md file in there.

As default, it should point to the local Hoop Gateway running on port 8009.

