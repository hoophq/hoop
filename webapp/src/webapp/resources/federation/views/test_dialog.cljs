(ns webapp.resources.federation.views.test-dialog
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Text]]
   ["lucide-react" :refer [CheckCircle Info XCircle]]
   [clojure.string :as str]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn- valid-email? [email]
  (and (string? email)
       (not (str/blank? email))
       (re-matches #"^[^@\s]+@[^@\s]+\.[^@\s]+$" email)))

(defn- test-error-display [test-result]
  (let [error-str (or (:error test-result) (:message test-result) (str test-result))
        probe-status (:probe_status test-result)]
    [:> Box {:class "rounded-lg border border-[--red-6] bg-[--red-2] overflow-hidden"}
     [:> Flex {:align "center" :gap "2" :class "px-3 py-2.5 bg-[--red-3]"}
      [:> XCircle {:size 16 :class "text-[--red-11] flex-shrink-0"}]
      [:> Text {:size "2" :weight "medium" :class "text-[--red-12] leading-none"} "Test failed"]]

     ;; left padding aligns the body under the title text, past the icon
     [:> Box {:class "px-3 py-3 pl-[2.375rem] space-y-2"}
      (when probe-status
        [:> Text {:size "1" :as "p" :class "text-[--red-11]"}
         "Probe status: "
         [:span {:class "font-mono"} probe-status]])

      [:> Box {:class "p-2 bg-[--gray-1] border border-[--red-6] rounded font-mono text-xs text-[--gray-12] whitespace-pre-wrap break-all max-h-48 overflow-auto"}
       error-str]]]))

(defn main [{:keys [conn-data]}]
  (r/with-let [current-user (rf/subscribe [:users->current-user])
               form-sub (rf/subscribe [:federation/form])
               test-status-sub (rf/subscribe [:federation/test-status])
               test-result-sub (rf/subscribe [:federation/test-result])
               email (r/atom nil)]

    (when (and (nil? @email)
               (get-in @current-user [:data :email]))
      (reset! email (get-in @current-user [:data :email])))

    (fn []
      (let [test-status @test-status-sub
            test-result @test-result-sub
            form @form-sub
            loading? (= test-status :loading)
            project-id (get-in form [:extra_config :project_id])
            target-template (:identity_target_template form)
            credentials? (or (get-in form [:admin_credentials_json])
                             (get-in @form-sub [:admin_credentials_json]))
            ready-to-run? (and (valid-email? @email)
                               (not (str/blank? project-id))
                               (not (str/blank? target-template))
                               (not loading?))

            close-modal #(do (rf/dispatch [:federation/reset-test])
                             (rf/dispatch [:modal->close]))

            run-test #(rf/dispatch [:federation/test @email conn-data])]

        [:> Box {:class "space-y-5 p-1"}
         [:> Box {:class "space-y-1"}
          [:> Heading {:size "5" :weight "bold" :class "text-[--gray-12]"}
           "Test as user"]
          [:> Text {:size "2" :class "text-[--gray-11]"}
           "Run the federation hook as a specific Hoop user and inspect what the session would receive."]]

         [forms/input
          {:label "Hoop user"
           :placeholder "user@example.com"
           :type "email"
           :required true
           :not-margin-bottom? true
           :value (or @email "")
           :on-change #(reset! email (-> % .-target .-value))}]

         [:> Callout.Root {:color "blue" :variant "soft" :size "1"}
          [:> Callout.Icon [:> Info {:size 14}]]
          [:> Callout.Text {:size "1"}
           "This is a dry run. No session is opened, no audit record is created."]]

         (case test-status
           :loading
           [:> Flex {:align "center" :gap "2" :class "text-[--gray-11]"}
            [:span {:class "inline-block w-4 h-4 border-2 border-[--gray-8] border-t-transparent rounded-full animate-spin"}]
            [:> Text {:size "2"} "Running test…"]]

           :success
           [:> Flex {:align "start" :gap "2" :class "text-[--green-11]"}
            [:> CheckCircle {:size 18 :class "mt-0.5 flex-shrink-0"}]
            [:> Box {:class "min-w-0 flex-1"}
             [:> Text {:size "2" :weight "medium" :as "p"} "Test passed"]
             (when-let [probe-status (:probe_status test-result)]
               [:> Text {:size "1" :class "text-[--gray-11] block"}
                "Probe: " probe-status])
             (when-let [output (or (:output test-result)
                                   (:stdout test-result)
                                   (:probe_output test-result))]
               [:> Box {:class "mt-2 p-2 bg-[--gray-3] rounded font-mono text-xs whitespace-pre-wrap break-all max-h-48 overflow-auto"}
                output])]]

           :error
           [test-error-display test-result]

           nil)

         [:> Flex {:justify "end" :gap "2"}
          [:> Button {:variant "soft"
                      :color "gray"
                      :type "button"
                      :on-click close-modal}
           "Cancel"]
          [:> Button {:variant "solid"
                      :type "button"
                      :disabled (not ready-to-run?)
                      :on-click run-test}
           "Run test"]]]))))
