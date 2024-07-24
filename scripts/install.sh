#!/bin/bash

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
    printf "${!1}%s${NC}\n" "$2"
}

# Function to print section headers
print_header() {
    printf "\n${BLUE}==== %s ====${NC}\n" "$1"
}

# ASCII Art Banner
print_banner() {
    echo "
██╗  ██╗ ██████╗  ██████╗ ██████╗    ██████╗ ███████╗██╗   ██╗
██║  ██║██╔═══██╗██╔═══██╗██╔══██╗   ██╔══██╗██╔════╝██║   ██║
███████║██║   ██║██║   ██║██████╔╝   ██║  ██║█████╗  ██║   ██║
██╔══██║██║   ██║██║   ██║██╔═══╝    ██║  ██║██╔══╝  ╚██╗ ██╔╝
██║  ██║╚██████╔╝╚██████╔╝██║     ██╗██████╔╝███████╗ ╚████╔╝ 
╚═╝  ╚═╝ ╚═════╝  ╚═════╝ ╚═╝     ╚═╝╚═════╝ ╚══════╝  ╚═══╝  
                                                              
    "
    echo "                   Welcome to HOOP.DEV Setup"
    echo "----------------------------------------------------------------"
}

# Print the banner at the start of the script
print_banner

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to handle existing installation
handle_existing_installation() {
    if [ -f docker-compose.yml ] || [ -f .env ]; then
        print_color "YELLOW" "Existing Hoop installation detected."
        read -p "Do you want to remove the existing installation and start fresh? (y/n): " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            print_color "YELLOW" "Removing existing installation..."
            docker compose down -v 2>/dev/null
            rm -f docker-compose.yml .env
            print_color "GREEN" "✔ Cleanup completed"
            return 0
        else
            print_color "GREEN" "✔ Keeping existing installation"
            return 1
        fi
    fi
    return 0
}

# Check for required commands
print_header "Checking Requirements"
for cmd in curl ifconfig docker; do
    if command_exists "$cmd"; then
        print_color "GREEN" "✔ $cmd is installed"
    else
        print_color "RED" "✘ $cmd is not installed. Please install it and try again."
        exit 1
    fi
done

# Handle existing installation
print_header "Checking for Existing Installation"
handle_existing_installation
existing_install=$?

# Step 1: Copy the compose file (if needed)
if [ "$existing_install" -eq 0 ] || [ ! -f docker-compose.yml ]; then
    print_header "Copying docker-compose.yml file"
    if curl -L https://raw.githubusercontent.com/hoophq/hoop/main/deploy/docker-compose/docker-compose.yml > ./docker-compose.yml 2>/dev/null; then
        print_color "GREEN" "✔ docker-compose.yml downloaded successfully"
    else
        print_color "RED" "✘ Failed to download docker-compose.yml"
        exit 1
    fi
else
    print_color "YELLOW" "Using existing docker-compose.yml file"
fi

# Step 2: Find local IP
print_header "Finding Local IP Address"
LOCAL_IP=$(ifconfig | grep "inet " | grep -Fv 127.0.0.1 | awk '{print $2}' | head -n 1)

if [ -z "$LOCAL_IP" ]; then
    print_color "RED" "✘ Could not determine local IP address"
    exit 1
fi

print_color "GREEN" "✔ Local IP address: $LOCAL_IP"

# Step 3: Set the .env file (if needed)
if [ "$existing_install" -eq 0 ] || [ ! -f .env ]; then
    print_header "Creating .env File"
    cat > .env <<EOF
HOOP_PUBLIC_HOSTNAME=$LOCAL_IP.nip.io
EOF
    print_color "GREEN" "✔ Created .env file with HOOP_PUBLIC_HOSTNAME=$LOCAL_IP.nip.io"
else
    print_color "YELLOW" "Using existing .env file"
    # Update HOOP_PUBLIC_HOSTNAME in existing .env file
    sed -i'' -e "s/^HOOP_PUBLIC_HOSTNAME=.*/HOOP_PUBLIC_HOSTNAME=$LOCAL_IP.nip.io/" .env
    print_color "GREEN" "✔ Updated HOOP_PUBLIC_HOSTNAME in existing .env file"
fi

# Step 4: Run docker compose
print_header "Running Docker Compose"
print_color "YELLOW" "Starting containers... (This may take a while)"
if docker compose up -d; then
    print_color "GREEN" "✔ Docker containers started successfully"
else
    print_color "RED" "✘ Failed to run docker compose"
    exit 1
fi

print_header "Setup Completed"
print_color "GREEN" "✔ Hoop setup completed successfully!"
print_color "YELLOW" "To view container logs, run: docker compose logs -f"
print_color "YELLOW" "To stop the containers, run: docker compose down"

print_header "Access and Get Started with hoop.dev"
echo "Follow these steps to access and set up your hoop.dev instance:"
echo

print_color "YELLOW" "1. Access in the Browser:"
echo "   Open your browser and go to:"
printf "${BOLD}${CYAN}   https://$LOCAL_IP.nip.io${NC}\n"
echo "   - If you see a warning about self-signed certificates, bypass it and proceed."
echo "   - If redirected to '/logout', remove this suffix from the URL and press enter."
echo

print_color "YELLOW" "2. Sign In to the developer portal:"
echo "   - Email:"
printf "${BOLD}${CYAN}     admin${NC}\n"
echo "   - Password:"
printf "${BOLD}${CYAN}     Password1${NC}\n"
echo "     (if this is a fresh installation)"
echo

print_color "YELLOW" "3. Initial Setup:"
echo "   - Skip the 2-factor authentication information (for fresh installations)."
echo "   - Change the default password when prompted (for fresh installations)."
echo

print_color "YELLOW" "4. After setup:"
echo "   You'll be redirected to the main page of the developer portal."
echo

print_color "YELLOW" "5. Next steps:"
echo "   - Set up your first user (if not already done)"
echo "   - Configure your first demo PostgreSQL connection (if not already configured)"
echo "   - Learn how to access it from the interface and a database client of your choice"
echo

echo "For more detailed instructions, refer to the hoop.dev documentation."
echo

print_color "GREEN" "Enjoy using hoop.dev!"