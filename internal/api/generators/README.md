# Message Types Code Generator

This package generates Go constants and helper functions for Port OS message interface types based on the YAML message definitions in the capability interfaces.

## What it generates

The generator creates the following in `message_types.gen.go`:

1. **Namespace constants**: Constants for each capability interface namespace
   ```go
   const (
       INTERFACE_PORTOS_CONVERSATION = "port1/systems/zero/capability-interfaces/conversation"
       INTERFACE_PORTOS_MEDIA_CONTROLS = "port1/systems/zero/capability-interfaces/media-controls"
       // ... more namespaces
   )
   ```

2. **Message name constants**: Constants for individual message names
   ```go
   const (
       INTERFACE_PORTOS_MEDIA_CONTROLS_PLAY = "play"
       INTERFACE_PORTOS_MEDIA_CONTROLS_PAUSE = "pause"
       // ... more messages
   )
   ```

3. **Message type constants**: Full message type constants for dispatch
   ```go
   const (
       INTERFACE_PORTOS_MEDIA_CONTROLS_PLAY_MESSAGE_TYPE = "port1/systems/zero/capability-interfaces/media-controls/play"
       // ... more message types
   )
   ```

4. **MessageTypeInfo map**: Runtime information about each message type
   ```go
   var MessageTypeInfo = map[string]MessageTypeInfo{
       "port1/systems/zero/capability-interfaces/media-controls/play": {
           ConstName: "INTERFACE_PORTOS_MEDIA_CONTROLS_PLAY",
           Namespace: "port1/systems/zero/capability-interfaces/media-controls",
           Name: "play",
           Version: "1.0",
           Type: "message",
           Directionality: "in",
           Title: "MediaControlsPlayMessageInterface",
           Description: "Schema for media play message payload validation",
       },
       // ... more message types
   }
   ```

5. **Helper functions**: Utility functions for working with message types
   - `GetMessageTypeInfo(namespace, messageName string) (MessageTypeInfo, bool)`
   - `GetMessageTypeInfoByFullType(fullType string) (MessageTypeInfo, bool)`
   - `IsValidMessageType(messageType string) bool`
   - `GetNamespaceFromMessageType(messageType string) string`
   - `GetMessageNameFromMessageType(messageType string) string`

## Usage in Dispatch Functions

You can now use the generated constants in your dispatch functions instead of hardcoded strings:

```go
func (l *LocalInstanceRuntime) Dispatch(ctx context.Context, payload api.Message) (ports.DispatchMessageResponse, error) {
    l.mu.Lock()
    defer l.mu.Unlock()
    
    switch ports.OrEmpty(payload.Type) {
    case api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAYER:
        return l.handleAudioMessage(ctx, payload)
    case api.INTERFACE_PORTOS_MEDIA_CONTROLS_PLAY_MESSAGE_TYPE:
        return l.handleMediaControlsPlayMessage(ctx, payload)
    case api.INTERFACE_PORTOS_MEDIA_CONTROLS_PAUSE_MESSAGE_TYPE:
        return l.handleMediaControlsPauseMessage(ctx, payload)
    case api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAY_MEDIA_MESSAGE_TYPE:
        return l.handleMediaPlayMessage(ctx, payload)
    default:
        return ports.DispatchMessageResponse{}, fmt.Errorf("request to dispatch to local a non supported message type: %s", ports.OrEmpty(payload.Type))
    }
}
```

## Running the Generator

The generator is automatically run as part of `go generate` in the api package. You can also run it manually:

```bash
cd api
go run generators/message_types.go
```

## Adding New Message Types

To add new message types:

1. Add new message definition YAML files in the appropriate capability interface directory
2. Run `go generate` in the api package
3. The new constants and types will be automatically generated

The generator reads the `components.schemas` entries of the public OpenAPI file (those with `x-type: message` or `attribute-definition`) and generates the appropriate Go code.
