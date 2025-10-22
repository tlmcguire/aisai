import democritus_strings

def vulnerable_netstring_encode(data):

    return democritus_strings.encode(data)

def vulnerable_netstring_decode(netstring):

    return democritus_strings.decode(netstring)

encoded = vulnerable_netstring_encode("Hello, World!")
print(encoded)
decoded = vulnerable_netstring_decode(encoded)
print(decoded)