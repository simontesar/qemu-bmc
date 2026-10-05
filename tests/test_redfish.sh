#!/bin/bash
# test_redfish.sh - Redfish API tests

test_redfish_port_listening() {
    local listen
    listen=$(container_exec ss -tlnp 2>/dev/null | grep ":443" || true)
    assert_contains "$listen" "443" "HTTPS 443 should be listening"
}

test_redfish_service_root() {
    local result
    result=$(redfish_get "/redfish/v1")
    assert_contains "$result" "@odata.type" "ServiceRoot should contain @odata.type"
    assert_contains "$result" "RedfishVersion" "ServiceRoot should contain RedfishVersion"
}

test_redfish_no_auth_rejected() {
    local status
    status=$(redfish_get_no_auth "/redfish/v1/Systems/1")
    assert_equals "401" "$status" "Unauthenticated request should return 401"
}

test_redfish_systems() {
    local result
    result=$(redfish_get "/redfish/v1/Systems/1")
    assert_contains "$result" "PowerState" "Systems/1 should contain PowerState"
}

test_redfish_power_off() {
    local status
    status=$(curl -sk -u "${IPMI_USER}:${IPMI_PASS}" \
        -X POST -H "Content-Type: application/json" \
        -d '{"ResetType":"ForceOff"}' \
        -o /dev/null -w "%{http_code}" \
        "https://${IPMI_HOST}:${REDFISH_PORT}/redfish/v1/Systems/1/Actions/ComputerSystem.Reset")
    assert_equals "200" "$status" "ForceOff should return 200"
    wait_for_power_state "off" 15
}

test_redfish_power_on() {
    local status
    status=$(curl -sk -u "${IPMI_USER}:${IPMI_PASS}" \
        -X POST -H "Content-Type: application/json" \
        -d '{"ResetType":"On"}' \
        -o /dev/null -w "%{http_code}" \
        "https://${IPMI_HOST}:${REDFISH_PORT}/redfish/v1/Systems/1/Actions/ComputerSystem.Reset")
    assert_equals "200" "$status" "On should return 200"
    wait_for_power_state "on" 30
}

test_redfish_managers() {
    local result
    result=$(redfish_get "/redfish/v1/Managers/1")
    assert_contains "$result" "@odata.type" "Managers/1 should contain @odata.type"
}

test_redfish_chassis() {
    local result
    result=$(redfish_get "/redfish/v1/Chassis/1")
    assert_contains "$result" "@odata.type" "Chassis/1 should contain @odata.type"
}

test_redfish_account_lifecycle() {
    local base="https://${IPMI_HOST}:${REDFISH_PORT}/redfish/v1/AccountService"
    local status location

    # Create: POST returns 201 with a Location for the new account
    location=$(curl -sk -u "${IPMI_USER}:${IPMI_PASS}" -o /dev/null -D - \
        -X POST -H "Content-Type: application/json" \
        -d '{"UserName":"ctuser","Password":"Abc123!x","RoleId":"ReadOnly"}' \
        "${base}/Accounts" | tr -d '\r' | awk -F': ' 'tolower($1)=="location" {print $2}')
    assert_contains "$location" "/redfish/v1/AccountService/Accounts/" "POST should return a Location header"

    # The new account can authenticate
    status=$(curl -sk -u 'ctuser:Abc123!x' -o /dev/null -w "%{http_code}" "$base")
    assert_equals "200" "$status" "New account should authenticate"

    # Update the password: old password rejected, new one accepted
    status=$(curl -sk -u "${IPMI_USER}:${IPMI_PASS}" -o /dev/null -w "%{http_code}" \
        -X PATCH -H "Content-Type: application/json" \
        -d '{"Password":"Xyz789?q"}' "https://${IPMI_HOST}:${REDFISH_PORT}${location}")
    assert_equals "200" "$status" "PATCH Password should return 200"
    status=$(curl -sk -u 'ctuser:Abc123!x' -o /dev/null -w "%{http_code}" "$base")
    assert_equals "401" "$status" "Old password should be rejected"
    status=$(curl -sk -u 'ctuser:Xyz789?q' -o /dev/null -w "%{http_code}" "$base")
    assert_equals "200" "$status" "New password should authenticate"

    # Delete: account is gone and can no longer authenticate
    status=$(curl -sk -u "${IPMI_USER}:${IPMI_PASS}" -o /dev/null -w "%{http_code}" \
        -X DELETE "https://${IPMI_HOST}:${REDFISH_PORT}${location}")
    assert_equals "204" "$status" "DELETE should return 204"
    status=$(redfish_get_status "$location")
    assert_equals "404" "$status" "Deleted account should return 404"
    status=$(curl -sk -u 'ctuser:Xyz789?q' -o /dev/null -w "%{http_code}" "$base")
    assert_equals "401" "$status" "Deleted account should be rejected"
}

# --- Run tests ---
start_test_container
wait_for_ipmi_ready 60
wait_for_redfish_ready 60

run_test test_redfish_port_listening
run_test test_redfish_service_root
run_test test_redfish_no_auth_rejected
run_test test_redfish_systems
run_test test_redfish_power_off
run_test test_redfish_power_on
run_test test_redfish_managers
run_test test_redfish_chassis
run_test test_redfish_account_lifecycle
