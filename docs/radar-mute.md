# Radar microphone privacy synchronization

Radar's kernel privacy driver handles the microphone button and its red
indicator. Independently toggling the application's saved mute flag can leave
the ring/controller opposite the button and prevent wake-word response.

When the kernel privacy interface exists, the application follows stable
`privacy_state` readback and waits through `privacy_timer_on` transitions.
Unreadable or unsettled state keeps software mute enabled. A saved mute is
restored with `privacy_trigger`, then confirmed through readback. The write
runs outside startup and transition locks because some kernels can block it.
Only the physical button releases hardware privacy. Devices without this
interface retain their existing software toggle and button GPIO behavior.

## Validation

Validated on a full-size second-generation Echo (Radar), 2026-09-28:

- Application OTA restart restored a saved mute, with matching kernel,
  persisted and controller state.
- The operator confirmed microphone-button/ring synchronization and wake
  response after pressing the physical button.
- Regression tests cover inversion, delayed transitions, persistence,
  unreadable state, saved-mute restoration, blocked writes and legacy toggles.

Cold boots and application restarts from both states still need broader
hardware testing. These observations do not establish support for every Radar
kernel or firmware variant.

## Diagnostic caution

Do not glob-read the `amz_privacy` directory. On the tested kernel, reading
`power_button_state` caused a NULL GPIO dereference and reboot. This binding
reads only `privacy_state` and `privacy_timer_on`.

The delayed-state contract was checked against the related Amazon kernel
[privacy driver](https://github.com/bengris32/android_kernel_amazon_rook/blob/master/drivers/misc/amz_priv.c)
and its keypad implementation; that source is not proven byte-identical to
the tested kernel. No vendor source or firmware assets are included here.

Radar porting context: [issue #535](https://github.com/wilbowes/EchoMuse/issues/535)
and [the existing Radar port](https://github.com/wilbowes/EchoMuse/pull/554).
