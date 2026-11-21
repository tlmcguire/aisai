def handle_third_party_invite(event, user_ip):

    request_url = event['invite_url']
    response = make_request_to_url(request_url, event['request_body'])

    print("Invite processed with response:", response)

def make_request_to_url(url, body):

    return "Response from {}".format(url)