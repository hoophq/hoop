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

# Function to get local IP address
get_local_ip() {
    if command_exists ifconfig; then
        ifconfig | grep -Eo 'inet (addr:)?([0-9]*\.){3}[0-9]*' | grep -Eo '([0-9]*\.){3}[0-9]*' | grep -v '127.0.0.1' | head -n 1
    elif command_exists ip; then
        ip addr show | grep -Eo 'inet (addr:)?([0-9]*\.){3}[0-9]*' | grep -Eo '([0-9]*\.){3}[0-9]*' | grep -v '127.0.0.1' | head -n 1
    else
        print_color "$RED" "Unable to determine IP address. Please manually set HOOP_PUBLIC_HOSTNAME in .env file."
        return 1
    fi
}

# Function to handle existing installation
handle_existing_installation() {
    if [ -f docker-compose.yml ] || [ -f .env ] || check_running_containers; then
        print_color "$YELLOW" "Existing Hoop installation detected."
        printf "${YELLOW}Remove existing installation and start fresh? (y/n): ${NC}"
        
        # Create a temporary file for user input
        tmp_file=$(mktemp)
        
        # Start background process to read input
        (
            read -r reply
            echo "$reply" > "$tmp_file"
        ) &
        
        # Wait for input (with timeout)
        sleep 30
        
        # Kill the background process if it's still running
        kill $! >/dev/null 2>&1
        
        # Read the reply from the temporary file
        reply=$(cat "$tmp_file")
        rm -f "$tmp_file"
        
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
            [Nn]*)
                print_color "$GREEN" "✔ Keeping existing installation"
                return 1
                ;;
            *)
                print_color "$RED" "Invalid or no input received. Keeping existing installation."
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

# Step 2: Find local IP
print_header "Finding Local IP Address"
LOCAL_IP=$(get_local_ip)

if [ -z "$LOCAL_IP" ]; then
    print_color "$RED" "✘ Could not determine local IP address"
    exit 1
fi

print_color "$GREEN" "✔ Local IP address: $LOCAL_IP"

# Step 3: Set the .env file (if needed)
if [ "$existing_install" = "0" ] || [ ! -f .env ]; then
    print_header "Creating .env File"
    cat > .env << EOF
HOOP_PUBLIC_HOSTNAME=$LOCAL_IP.nip.io
EOF
    print_color "$GREEN" "✔ Created .env file with HOOP_PUBLIC_HOSTNAME=$LOCAL_IP.nip.io"
else
    print_color "$YELLOW" "Using existing .env file"
    # Update HOOP_PUBLIC_HOSTNAME in existing .env file
    sed -i.bak "s/^HOOP_PUBLIC_HOSTNAME=.*/HOOP_PUBLIC_HOSTNAME=$LOCAL_IP.nip.io/" .env && rm -f .env.bak
    print_color "$GREEN" "✔ Updated HOOP_PUBLIC_HOSTNAME in existing .env file"
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
echo "Follow these steps to access and set up your hoop.dev instance:"
echo

print_color "$YELLOW" "1. Access in the Browser:"
echo "   Open your browser and go to:"
printf "${BOLD}${CYAN}   https://%s.nip.io${NC}\n" "$LOCAL_IP"
echo "   - If you see a warning about self-signed certificates, bypass it and proceed."
echo "   - If redirected to '/logout', remove this suffix from the URL and press enter."
echo

print_color "$YELLOW" "2. Sign In to the developer portal:"
echo "   - Email:"
printf "${BOLD}${CYAN}     admin${NC}\n"
echo "   - Password:"
printf "${BOLD}${CYAN}     Password1${NC}\n"
echo "     (if this is a fresh installation)"
echo

print_color "$YELLOW" "3. Initial Setup:"
echo "   - Skip the 2-factor authentication information (for fresh installations)."
echo "   - Change the default password when prompted (for fresh installations)."
echo

print_color "$YELLOW" "4. After setup:"
echo "   You'll be redirected to the main page of the developer portal."
echo

print_color "$YELLOW" "5. Next steps:"
echo "   - Set up your first user (if not already done)"
echo "   - Configure your first demo PostgreSQL connection (if not already configured)"
echo "   - Learn how to access it from the interface and a database client of your choice"
echo

echo "For more detailed instructions, refer to the hoop.dev documentation."
echo

print_color "$GREEN" "Enjoy using hoop.dev!"