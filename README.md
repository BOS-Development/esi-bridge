# ESI Proxy

Proxies all Eve Online ESI connections, allowing applications to access character lists and ESI endpoints without needing to be explicitly added.  

## Primary Workflow

* User adds character to proxy via ESI
* User can define which accounts characters belong too
* User configures external application (MCO, Pyfa) to use the proxy
* External Application makes a request to use the proxy, the user is prompted to approve and to list what data the application is allowed to see
* External Applications use the proxy for all requests to ESI
* Includes endpoints to list accounts & characters
* Proxy caches all ESI responses, and provides a cached response unless the ESI cache is expired for the endpoint
* Proxy maintains access tokens

## Implementation

* Cross-Platform, Windows, MacOS and Linux
* SQLite
* Ability to copy token information from one computer to another
