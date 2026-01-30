(ns webapp.parallel-mode.db)

;; Schema for parallel mode state
(def default-state
  {:modal {:open? false
           :search-term ""}
   :selection {:connections []           ; Vector of selected connections
               :draft-connections nil}   ; Draft state (saved when opening modal)
   :execution {:search-term ""           ; Search in execution summary
               :active-tab "success"}})  ; Active tab: success | error

;; Constants
(def min-connections 2)
(def excluded-subtypes #{"tcp" "httpproxy" "grafana" "kibana" "ssh" "git" "github"})

