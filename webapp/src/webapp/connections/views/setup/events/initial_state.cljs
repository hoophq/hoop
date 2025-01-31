(ns webapp.connections.views.setup.events.initial-state)

(def initial-state
  {:type nil
   :subtype nil
   :current-step :resource
   :name ""
   :tags []
   :command ""
   :tags-input ""
   :database-type nil
   :database-credentials {}
   :app-type nil
   :os-type nil
   :agent-id nil
   :network-type nil
   :network-credentials {}
   :credentials {:current-key ""
                 :current-value ""
                 :current-file-name ""
                 :current-file-content ""
                 :environment-variables []
                 :configuration-files []}
   :config {:review false
            :data-masking false
            :database-schema false
            :review-groups []
            :data-masking-types []
            :access-modes {:runbooks true
                           :native true
                           :web true}}})
