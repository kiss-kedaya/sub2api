Account notification rules, durable threshold episodes, encrypted webhook
configuration, delivery receipts, and the corresponding admin UI were adapted
from ranxi2001/sub2api at production commit
9620d315f72a7dbf04fa10fca02970e1e410805e under the project's LGPL-3.0 license.

This fork retains its existing Bark and balance alert services. Notification
destinations are empty and the feature is disabled by default. Automatic
concurrency changes and external New API login/group discovery were separated
from this module. Upstream balance reads require an enabled rule and use the
existing account probe's authenticated, validated route and identity CAS.
