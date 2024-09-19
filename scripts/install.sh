#!/bin/sh

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Function to print colored output
print_color() {
    printf "%b%s%b\n" "$1" "$2" "$NC"
}

# Function to print section headers
print_header() {
    printf "\n%b==== %s ====%b\n" "$BLUE" "$1" "$NC"
}

# ASCII Art Banner
print_banner() {
    cat << "EOF"

██╗  ██╗ ██████╗  ██████╗ ██████╗    ██████╗ ███████╗██╗   ██╗
██║  ██║██╔═══██╗██╔═══██╗██╔══██╗   ██╔══██╗██╔════╝██║   ██║
███████║██║   ██║██║   ██║██████╔╝   ██║  ██║█████╗  ██║   ██║
██╔══██║██║   ██║██║   ██║██╔═══╝    ██║  ██║██╔══╝  ╚██╗ ██╔╝
██║  ██║╚██████╔╝╚██████╔╝██║     ██╗██████╔╝███████╗ ╚████╔╝ 
╚═╝  ╚═╝ ╚═════╝  ╚═════╝ ╚═╝     ╚═╝╚═════╝ ╚══════╝  ╚═══╝  
                                                              
EOF
    echo "                   Welcome to HOOP.DEV Setup"
    echo "----------------------------------------------------------------"
}

# Print the banner at the start of the script
print_banner

# Function to check if a command exists
command_exists() {
    command -v "$1" > /dev/null 2>&1
}

# Function to check for running hoop.dev containers
check_running_containers() {
    docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^hoop' || \
    docker-compose ps --services 2>/dev/null | grep -q '^hoop'
}

# Function to handle existing installation
handle_existing_installation() {
    if [ -f docker-compose.yml ] || [ -f .env ] || check_running_containers; then
        print_color "$YELLOW" "Existing Hoop installation detected."

        if [ -t 0 ]; then
            # Running interactively
            printf "${YELLOW}Remove existing installation and start fresh? (y/n): ${NC}"
            read -r reply
        else
            # Non-interactive mode
            if [ -n "$HOOP_CLEAN_INSTALL" ] && [ "$HOOP_CLEAN_INSTALL" = "yes" ]; then
                reply="y"
            else
                reply="n"
            fi
            print_color "$YELLOW" "Non-interactive mode: $reply"
        fi

        case "$reply" in
            [Yy]*)
                print_color "$YELLOW" "Removing existing installation..."
                if command_exists docker-compose; then
                    docker-compose down -v 2>/dev/null
                else
                    docker compose down -v 2>/dev/null
                fi
                rm -f docker-compose.yml .env
                print_color "$GREEN" "✔ Cleanup completed"
                return 0
                ;;
            *)
                print_color "$GREEN" "✔ Keeping existing installation"
                return 1
                ;;
        esac
    else
        return 0
    fi
}

# Check for required commands
print_header "Checking Requirements"
for cmd in curl docker; do
    if command_exists "$cmd"; then
        print_color "$GREEN" "✔ $cmd is installed"
    else
        print_color "$RED" "✘ $cmd is not installed. Please install it and try again."
        exit 1
    fi
done

# Check Docker Compose
if command_exists docker-compose; then
    print_color "$GREEN" "✔ docker-compose is installed"
    DOCKER_COMPOSE_CMD="docker-compose"
elif docker compose version > /dev/null 2>&1; then
    print_color "$GREEN" "✔ docker compose plugin is installed"
    DOCKER_COMPOSE_CMD="docker compose"
else
    print_color "$RED" "✘ Neither docker-compose nor docker compose plugin is installed. Please install one and try again."
    exit 1
fi

# Handle existing installation
print_header "Checking for Existing Installation"
handle_existing_installation
existing_install=$?

# Step 1: Copy the compose file (if needed)
if [ "$existing_install" = "0" ] || [ ! -f docker-compose.yml ]; then
    print_header "Copying docker-compose.yml file"
    if curl -L https://raw.githubusercontent.com/hoophq/hoop/main/deploy/docker-compose/docker-compose.yml > ./docker-compose.yml 2>/dev/null; then
        print_color "$GREEN" "✔ docker-compose.yml downloaded successfully"
    else
        print_color "$RED" "✘ Failed to download docker-compose.yml"
        exit 1
    fi
else
    print_color "$YELLOW" "Using existing docker-compose.yml file"
fi

# Step 3: Set the .env file (if needed)
if [ "$existing_install" = "0" ] || [ ! -f .env ]; then
    print_header "Creating .env File"
    cat > .env << EOF
JWT_SECRET_KEY=$(LC_ALL=C tr -dc A-Za-z0-9_ < /dev/urandom | head -c 43 | xargs)
EOF

    print_color "$GREEN" "✔ Created .env file with randomly generated JWT_SECRET_KEY. Access the .env file to change it if you want."
else
  print_color "$YELLOW" "Using existing .env file"
  # Check if JWT_SECRET_KEY exists and is not empty in the .env file and generate one if not
  if grep -q "^JWT_SECRET_KEY=" .env; then
    JWT_VALUE=$(grep "^JWT_SECRET_KEY=" .env | cut -d '=' -f2)
    if [ -z "$JWT_VALUE" ]; then
      print_color "$YELLOW" "JWT_SECRET_KEY is empty. Generating new value..."
      sed -i 's/^JWT_SECRET_KEY=.*/JWT_SECRET_KEY='$(LC_ALL=C tr -dc A-Za-z0-9_ < /dev/urandom | head -c 43 | xargs)'/' .env
      print_color "$GREEN" "✔ New JWT_SECRET_KEY generated"
    fi
  else
    print_color "$YELLOW" "Generating JWT_SECRET_KEY..."
    echo "JWT_SECRET_KEY=$(LC_ALL=C tr -dc A-Za-z0-9_ < /dev/urandom | head -c 43 | xargs)" >> .env
    print_color "$GREEN" "✔ JWT_SECRET_KEY generated"
  fi
fi

# Step 4: Run docker compose
print_header "Running Docker Compose"
print_color "$YELLOW" "Starting containers... (This may take a while)"
if $DOCKER_COMPOSE_CMD up -d; then
    print_color "$GREEN" "✔ Docker containers started successfully"
else
    print_color "$RED" "✘ Failed to run docker compose"
    exit 1
fi

print_header "Setup Completed"
print_color "$GREEN" "✔ Hoop setup completed successfully!"
print_color "$YELLOW" "To view container logs, run: $DOCKER_COMPOSE_CMD logs -f"
print_color "$YELLOW" "To stop the containers, run: $DOCKER_COMPOSE_CMD down"

print_header "Access and Get Started with hoop.dev"
echo "Follow these steps to access your hoop.dev instance:"
echo

print_color "$YELLOW" "1. Access in the Browser:"
echo "   Open your browser and go to:"
printf "${BOLD}${CYAN}   https://localhost:8009${NC} or, if you are on a VM, access your VM public IP at port 8009 (or any port you bind to the internal 8009\n"
echo "   - If redirected to '/logout', remove this suffix from the URL and press enter."
echo

print_color "$YELLOW" "2. At the login page, click at \"Create a an account\" and create an new account with your email and password"

print_color "$YELLOW" "3. Initial Setup:"
echo "   - Click at the Quick Start button to set up a test PostgreSQL connection."
echo "   - Run some queries and have some fun."
echo

echo "For more detailed instructions, refer to the hoop.dev documentation."
echo

print_color "$GREEN" "Enjoy using hoop.dev!"

