(ns webapp.sessions.components.session-header
  (:require
   ["@radix-ui/themes" :refer [Box Button IconButton Flex Heading]]
   ["lucide-react" :refer [Link2 Square RotateCw X Workflow]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.routes :as routes]))

(def ^:private redacted-param-prefix
  ;; the gateway writes this in place of a password or file input
  ;; (encodeRequestParams in gateway/api/runbooks/runbooksV2.go)
  "[param redacted. type = ")

(defn redacted-params?
  "True when the runbookParameters label holds a redacted value: a re-run
  cannot send it back, the user must enter the value again in the form."
  [params-json]
  (let [params (try (js->clj (js/JSON.parse params-json)) (catch :default _ nil))]
    (boolean (some #(and (string? %) (cs/starts-with? % redacted-param-prefix))
                   (when (map? params) (vals params))))))

(defn- open-runbook-form [labels]
  (rf/dispatch [:modal->close])
  (rf/dispatch [:navigate :runbooks {:runbook (:runbookFile labels)
                                     :repository (:runbookRepository labels)}])
  (rf/dispatch [:show-snackbar {:level :info
                                :text "Enter the hidden inputs again"
                                :description (str "This runbook has password or file inputs. "
                                                  "Their values are not stored, so the run starts from its form.")}]))

(defn- re-run-session [session]
  (cond
    (and (-> session :labels :runbookFile)
         (redacted-params? (-> session :labels :runbookParameters)))
    (open-runbook-form (:labels session))

    (-> session :labels :runbookFile)
    (do
      (let [labels (:labels session)
            file-name (:runbookFile labels)
            params (js/JSON.parse (:runbookParameters labels))
            connection-name (:connection session)
            repository (:runbookRepository labels)
            on-success (fn [res]
                         (rf/dispatch [:audit->get-session-by-id {:id (:session_id res) :verb "exec"}])
                         (rf/dispatch [:audit->get-sessions]))
            on-failure (fn [_error-message error]
                         (rf/dispatch [:audit->get-session-by-id {:id (:session_id error) :verb "exec"}])
                         (rf/dispatch [:audit->get-sessions]))]
        (rf/dispatch [:runbooks/exec {:file-name file-name
                                      :params params
                                      :connection-name connection-name
                                      :repository repository
                                      :correlation-id (:correlation_id session)
                                      :on-success on-success
                                      :on-failure on-failure}]))
      (rf/dispatch [:audit->clear-session-details-state {:status :loading}]))

    :else
    (do
      (rf/dispatch [:jira-integration->get])
      (rf/dispatch [:audit->re-run-session session]))))

(def killing-status (r/atom :ready))

(defn- kill-session [session]
  (reset! killing-status :loading)
  (rf/dispatch [:audit->kill-session session killing-status]))

(defn main [{:keys [session user on-close clipboard-disabled?]}]
  (let [admin? (:admin? user)
        current-user-id (:id user)
        session-user-id (:user_id session)
        session-status (:status session)
        is-session-owner? (= session-user-id current-user-id)
        can-rerun? (and (= (:verb session) "exec")
                        (nil? (-> session :integrations_metadata :jira_issue_url)))
        session-url (str (-> js/document .-location .-origin)
                         (routes/url-for :sessions)
                         "/" (:id session))
        correlation-id (:correlation_id session)
        has-workflow? (and correlation-id (not (cs/blank? correlation-id)))
        can-kill-session? (and (or is-session-owner?
                                   admin?)
                               (= (:verb session) "exec")
                               (not (= session-status "done")))
        current-path (.-pathname (.-location js/window))
        is-dedicated-page? (cs/starts-with? current-path "/sessions/")
        copy-to-clipboard (fn []
                            (-> (js/navigator.clipboard.writeText session-url)
                                (.then #(rf/dispatch [:show-snackbar {:level :success :text "URL copied to clipboard"}]))
                                (.catch #(js/console.error "Failed to copy:" %))))]
    [:> Box {:class "sticky top-app-header z-10 bg-white pt-5 pb-5 -mt-6"}
     [:> Flex {:justify "between" :align "start"}
      ;; Left side - Title and role
      [:> Heading {:as "h2" :size "5" :weight "bold" :class "text-gray-12"}
       "Session Details"]

      ;; Right side - Action buttons
      [:> Flex {:gap "2" :align "center"}
       ;; View Timeline button (when session has a correlation id)
       (when has-workflow?
         [:> Button
          {:on-click (if is-dedicated-page?
                       #(rf/dispatch [:navigate :workflow-details
                                      {}
                                      :correlation-id
                                      (js/encodeURIComponent correlation-id)])
                       #(js/open
                         (str (-> js/document .-location .-origin)
                              (routes/url-for :workflow-details
                                              :correlation-id
                                              (js/encodeURIComponent correlation-id)))
                         "_blank"))
           :variant "soft"
           :size "2"
           :color "gray"}
          [:> Workflow {:size 20 :class "text-gray-11"}]
          "View Timeline"])

       ;; Kill session button
       (when can-kill-session?
         [:div {:class "relative group"}
          [:> Button {:on-click #(kill-session session)
                      :variant "soft"
                      :size "2"
                      :color "red"}
           (if (= @killing-status :loading)
             [loaders/simple-loader {:size 2}]
             [:> Square {:size 20 :class "text-red-600"}])
           "Kill Session"]])

       ;; Re-run button
       (when can-rerun?
         [:> Button
          {:on-click #(re-run-session session)
           :variant "soft"
           :size "2"
           :color "gray"}
          [:> RotateCw {:size 20 :class "text-gray-11"}]
          "Re-run"])

       ;; Share button (copy link)
       (when-not clipboard-disabled?
         [:> Button
          {:on-click copy-to-clipboard
           :variant "soft"
           :size "2"
           :color "gray"}
          [:> Link2 {:size 20 :class "text-gray-11"}]
          "Share"])

       ;; Close button - only show when in modal context (not on dedicated page)
       (when-not is-dedicated-page?
         [:> IconButton
          {:on-click on-close
           :variant "soft"
           :size "2"
           :color "gray"}
          [:> X {:size 20 :class "text-gray-11"}]])]]]))
