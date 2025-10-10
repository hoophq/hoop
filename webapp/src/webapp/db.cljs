(ns webapp.db
  (:require [clojure.edn :refer [read-string]]))

(def default-db
  {:agents {:status :loading, :data []}
   :agents-embedded []
   :ai-data-masking {:list {:status :idle :data []}
                     :active-rule {:status :idle :data nil}
                     :submitting? false}
   :ask-ai->question-responses []
   :audit->session-details {:status :loading, :session nil, :session-logs {:status :loading}}
   :audit->session-logs {:status :idle, :data nil}
   :audit->filtered-session-by-id {:status :loading, :data []}
   :connections {:loading true :details {}}
   :connections->connection-connected {:status :loading, :data nil}
   :connections->updating-connection {:loading true, :data []}
   :native-client-access {:requesting? false, :current nil}
   :aws-connect {:status :not-started
                 :current-step :credentials
                 :credentials {:type nil
                               :iam-role nil
                               :iam-user {:access-key-id nil
                                          :secret-access-key nil
                                          :region nil
                                          :session-token nil}}
                 :accounts {:selected #{}
                            :data []}
                 :resources {:selected #{}
                             :data []
                             :errors #{}}
                 :agents {:assignments {}}}
   :connection-setup {:type nil
                      :subtype nil
                      :current-step :type
                      :credentials {}
                      :tags {}
                      :command-args []}
   :database-schema {:status :loading, :schema-tree nil, :indexes-tree nil}
   :dialog {:status :closed
            :type :info
            :on-success nil
            :text ""
            :text-action-button ""
            :action-button? true
            :title ""}
   :dialog-on-success nil
   :dialog-status :closed
   :dialog-text ""
   :dialog-title ""
   :draggable-card {:status :closed, :component nil, :on-click-close nil, :on-click-expand nil}
   :editor {}
   :editor-plugin->connections-exec-list {:status :ready :data nil}
   :editor-plugin->connections-runbook-list {:status :ready :data nil}
   :editor-plugin->current-connection {:status :loading :data nil}
   :editor-plugin->select-language "shell"
   :editor-plugin->script []
   :gateway->info {:loading true, :data nil}
   :guardrails->active-guardrail {:action nil
                                  :name ""
                                  :description ""
                                  :input [{:type "" :rule "" :details ""}]
                                  :output [{:type "" :rule "" :details ""}]}
   :guardrails->connections-list {:status :loading :data []}
   :jira-integration->details {:loading true, :data {}}
   :modal-radix {:open? false, :content nil}
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
   :users->current-user {:loading true, :data nil}
   :webclient->active-panel nil
   :command-palette {:open? false
                     :query ""
                     :current-page :main
                     :context {}
                     :search-results {:status :idle :data {}}}
   :runbooks {:connection-dialog-open? false
              :execute-trigger false
              :selected-connection nil}
   :roles {:data []
           :loading false
           :current-page 1
           :page-size 20
           :has-more? false
           :total-count 0}})
