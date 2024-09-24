(ns webapp.db
  (:require [clojure.edn :refer [read-string]]))

(def default-db
  {:agents {:status :loading, :data []}
   :agents-embedded []
   :ask-ai->question-responses []
   :audit->session-details {:status :loading, :session nil, :session-logs {:status :loading}}
   :audit->filtered-session-by-id {:status :loading, :data []}
   :connections {:loading true}
   :connections->connection-connected {:status :loading, :data nil}
   :connections->updating-connection {:loading true, :data []}
   :database-schema {:status :loading, :schema-tree nil, :indexes-tree nil}
   :dialog {:status :closed
            :type :info
            :on-success nil
            :text ""
            :text-action-button ""
            :title ""}
   :dialog-on-success nil
   :dialog-status :closed
   :dialog-text ""
   :dialog-title ""
   :draggable-card {:status :closed, :component nil, :on-click-close nil, :on-click-expand nil}
   :editor-plugin->connections-exec-list {:status :ready :data nil}
   :editor-plugin->connections-runbook-list {:status :ready :data nil}
   :editor-plugin->current-connection {:status :loading :data nil}
   :editor-plugin->filtered-run-connection-list nil
   :editor-plugin->run-connection-list {:status :loading :data nil}
   :editor-plugin->run-connection-list-selected (or (read-string (.getItem js/localStorage "run-connection-list-selected")) nil)
   :editor-plugin->select-language "shell"
   :editor-plugin->script []
   :gateway->info {:loading true, :data nil}
   :modal-status :closed
   :new-task->selected-connection nil
   :new-task nil
   :organization->api-key {:status :loading, :data nil}
   :page-loader-status :open
   :plugins->active-tab :plugins-store
   :plugins->plugin-details {:status :loading, :plugin {}}
   :reports->session {:status :loading, :data nil}
   :routes->route (.-pathname (.-location js/window))
   :runbooks-plugin->selected-runbooks {:status :idle, :data nil}
   :runbooks-plugin->runbooks {:status :idle, :data nil}
   :runbooks-plugin->runbooks-by-connection {:status :loading, :data nil, :message ""}
   :segment->analytics nil
   :sidebar-desktop {:status (or (keyword (.getItem js/localStorage "sidebar")) :closed)}
   :sidebar-mobile {:status :closed}
   :snackbar-level :error
   :snackbar-text "Default text"
   :updating-connection {:loading true, :data []}
   :user nil
   :user-groups []
   :users []
   :users->current-user {:loading true, :data nil}})
