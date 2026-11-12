# Fetch the timezone using ipapi and store it in a variable
TIMEZONE=$(curl -s "http://ip-api.com/json/?fields=timezone" | sed 's/{"timezone":"\([^"]*\)"}/\1/')

# Check if the timezone was fetched successfully and try to set it
if [ -z "$TIMEZONE" ] || ! timedatectl set-timezone "$TIMEZONE"; then
    echo "Failed to fetch or set timezone"
else
    echo "Timezone successfully set to $TIMEZONE"
    timedatectl status
fi
