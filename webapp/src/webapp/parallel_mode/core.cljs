(ns webapp.parallel-mode.core
  "Core namespace for parallel mode - registers all events and subscriptions"
  (:require
   [webapp.parallel-mode.events.modal]
   [webapp.parallel-mode.events.selection]
   [webapp.parallel-mode.events.execution]
   [webapp.parallel-mode.events.submit]
   [webapp.parallel-mode.subs]))

;; This namespace just requires all the event and subscription namespaces
;; to ensure they are loaded and registered with re-frame

