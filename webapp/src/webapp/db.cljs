(ns webapp.db
  (:require [webapp.parallel-mode.db :as parallel-mode-db]))

(def default-db
  {:agents {:status :loading, :data []}
   :ai-data-masking {:list {:status :idle :data []}
                     :active-rule {:status :idle :data nil}
                     :submitting? false}
   :audit->session-details {:status :loading, :session nil, :session-logs {:status :loading}}
   :audit->session-logs {:status :idle, :data nil}
   :audit->filtered-session-by-id {:status :idle, :data [] :errors [] :search-term "" :offset 0 :has-more? false :loading false}
   :connections {:loading true :details {}}
   :connections->pagination {:data []
                             :loading false
                             :current-page 1
                             :page-size 50
                             :has-more? false
                             :total 0}
   :native-client-access {:requesting-connections #{}
                          :sessions {}}
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
   :draggable-cards {}
   :editor {}
   :editor-plugin->script []
   :gateway->info {:loading true, :data nil}
   :guardrails->active-guardrail {:action nil
                                  :name ""
                                  :description ""
                                  :input [{:type "" :rule "" :details ""}]
                                  :output [{:type "" :rule "" :details ""}]}
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
   :runbooks->selected-runbooks {:status :idle, :data nil}
   :runbooks->filtered-runbooks []
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
   :parallel-mode parallel-mode-db/default-state
   :command-palette {:open? false
                     :query ""
                     :current-page :main
                     :context {}
                     :search-results {:status :idle :data {}}}
   :runbooks {:connection-dialog-open? false
              :selected-connection nil
              :list {:status :idle :data [] :error nil}
              :metadata []
              :metadata-key ""
              :metadata-value ""
              :keep-metadata? false}
   :runbooks->exec {:status :idle :data nil}
   :runbooks-rules {:list {:status :idle :data [] :error nil}
                    :active-rule {:status :idle :data nil :error nil}}})
